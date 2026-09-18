package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// internalOnlyPayloadKeys lists payload keys that exist purely for in-process
// listeners and must never be serialized to a WebSocket client.
//
// `issue:updated` carries prev_description and prev_title so the in-process
// listeners can diff against the new values: subscriber_listeners.go adds newly
// @mentioned users, notification_listeners.go builds mention notifications, and
// activity_listeners.go records the title change. Those all run on
// bus.Subscribe, which Publish dispatches BEFORE the SubscribeAll forwarder
// below, so removing the keys on the way out cannot affect them.
//
// No client reads either key — IssueUpdatedPayload in
// packages/core/types/events.ts does not declare them. They reached the wire
// only because the forwarder reuses the producer's payload map verbatim, which
// meant every description autosave broadcast TWO full copies of the description
// (the new one inside `issue`, plus prev_description) to every connection in the
// workspace, including users who did not have the issue open. The DB write is
// O(1); the fanout was O(workspace connections × description size) (MUL-5492).
//
// This is a table rather than an `if` on one event type because the bug was
// structural, not a typo: the next large field added to a published payload
// inherits the same cost silently. Keeping the list declarative puts the
// internal/external payload boundary in one reviewable place.
var internalOnlyPayloadKeys = map[string][]string{
	protocol.EventIssueUpdated: {"prev_description", "prev_title"},
	// task:failed error text is consumed synchronously by channel outbounds.
	// It may contain provider/runtime detail that belongs in the originating
	// chat transcript, not in the workspace-wide realtime fanout.
	protocol.EventTaskFailed: {"error"},
}

// projectOutbound returns payload with the event type's internal-only keys
// removed, ready to serialize for external consumers.
//
// The input map is never mutated. In-process listeners have already run by the
// time this is called, but the producer still owns the map and a second
// forwarder may yet read it, so mutating it in place would be a landmine.
func projectOutbound(eventType string, payload any) any {
	keys := internalOnlyPayloadKeys[eventType]
	if len(keys) == 0 {
		return payload
	}
	m, ok := payload.(map[string]any)
	if !ok {
		return payload
	}
	projected := make(map[string]any, len(m))
	for k, v := range m {
		projected[k] = v
	}
	for _, k := range keys {
		delete(projected, k)
	}
	return projected
}

// registerListeners wires up event bus listeners for WS broadcasting.
// Personal events (inbox, invites) are sent only to the target user via
// SendToUser. All other events are broadcast to the workspace room.
//
// The broadcaster parameter is intentionally typed as the realtime.Broadcaster
// interface (not *realtime.Hub) so that this layer can later be swapped out
// for a Redis-backed relay or a feature-flagged dual-write implementation
// without touching any of the event listeners below. This is Phase 0 of the
// horizontal-scaling plan tracked in MUL-1138.
func registerListeners(bus *events.Bus, b realtime.Broadcaster) {
	// Personal events should NOT be broadcast to the whole workspace.
	personalEvents := map[string]bool{
		protocol.EventInboxNew:           true,
		protocol.EventInboxRead:          true,
		protocol.EventInboxArchived:      true,
		protocol.EventInboxUnarchived:    true,
		protocol.EventInboxBatchRead:     true,
		protocol.EventInboxBatchArchived: true,
		protocol.EventInvitationCreated:  true,
		protocol.EventInvitationRevoked:  true,
	}

	registerPersonalEventListeners(bus, b)
	registerWorkspaceBroadcastListener(bus, b, personalEvents)
}

func sendEventToRecipient(b realtime.Broadcaster, e events.Event, recipientID string) {
	if recipientID == "" {
		return
	}
	data, err := json.Marshal(map[string]any{"type": e.Type, "payload": projectOutbound(e.Type, e.Payload), "actor_id": e.ActorID, "actor_type": e.ActorType})
	if err != nil {
		return
	}
	realtime.M.RecordEvent(e.Type)
	b.SendToUser(recipientID, data)
}

func registerPersonalEventListeners(bus *events.Bus, b realtime.Broadcaster) {
	// inbox:new — extract recipient from nested item
	bus.Subscribe(protocol.EventInboxNew, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		item, ok := payload["item"].(map[string]any)
		if !ok {
			return
		}
		recipientID, _ := item["recipient_id"].(string)
		sendEventToRecipient(b, e, recipientID)
	})

	// inbox:read, inbox:archived, inbox:unarchived, inbox:batch-read,
	// inbox:batch-archived — extract recipient from top-level payload
	for _, eventType := range []string{
		protocol.EventInboxRead, protocol.EventInboxArchived, protocol.EventInboxUnarchived,
		protocol.EventInboxBatchRead, protocol.EventInboxBatchArchived,
	} {
		bus.Subscribe(eventType, func(e events.Event) {
			payload, ok := e.Payload.(map[string]any)
			if !ok {
				return
			}
			recipientID, _ := payload["recipient_id"].(string)
			sendEventToRecipient(b, e, recipientID)
		})
	}

	// invitation:created — send to the invitee so they see the invitation in real time.
	bus.Subscribe(protocol.EventInvitationCreated, func(e events.Event) {
		handleInvitationCreatedListener(b, e)
	})

	// invitation:revoked — send to the invitee so their pending list updates.
	bus.Subscribe(protocol.EventInvitationRevoked, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		uid, _ := payload["invitee_user_id"].(*string)
		if uid != nil && *uid != "" {
			sendEventToRecipient(b, e, *uid)
		}
	})

	// member:added — also send to the invited user so they discover the new workspace.
	bus.Subscribe(protocol.EventMemberAdded, func(e events.Event) {
		handleMemberAddedListener(b, e)
	})
}

func handleInvitationCreatedListener(b realtime.Broadcaster, e events.Event) {
	payload, ok := e.Payload.(map[string]any)
	if !ok {
		return
	}
	inv, ok := payload["invitation"].(handler.InvitationResponse)
	if !ok {
		// Fallback for map encoding.
		if invMap, ok := payload["invitation"].(map[string]any); ok {
			if uid, _ := invMap["invitee_user_id"].(*string); uid != nil && *uid != "" {
				sendEventToRecipient(b, e, *uid)
			}
		}
		return
	}
	if inv.InviteeUserID != nil && *inv.InviteeUserID != "" {
		sendEventToRecipient(b, e, *inv.InviteeUserID)
	}
}

func handleMemberAddedListener(b realtime.Broadcaster, e events.Event) {
	payload, ok := e.Payload.(map[string]any)
	if !ok {
		return
	}
	var userID string
	switch m := payload["member"].(type) {
	case handler.MemberWithUserResponse:
		userID = m.UserID
	case map[string]any:
		userID, _ = m["user_id"].(string)
	default:
		slog.Warn("member:added: unexpected member payload type", "type", fmt.Sprintf("%T", payload["member"]))
	}
	if userID == "" {
		return
	}
	data, err := json.Marshal(map[string]any{"type": e.Type, "payload": projectOutbound(e.Type, e.Payload), "actor_id": e.ActorID, "actor_type": e.ActorType})
	if err != nil {
		return
	}
	realtime.M.RecordEvent(e.Type)
	b.SendToUser(userID, data, e.WorkspaceID)
}

func registerWorkspaceBroadcastListener(bus *events.Bus, b realtime.Broadcaster, personalEvents map[string]bool) {
	bus.SubscribeAll(func(e events.Event) {
		// Skip personal events — they are handled by type-specific listeners above.
		if personalEvents[e.Type] {
			return
		}

		msg := map[string]any{
			"type":       e.Type,
			"payload":    projectOutbound(e.Type, e.Payload),
			"actor_id":   e.ActorID,
			"actor_type": e.ActorType,
		}
		data, err := json.Marshal(msg)
		if err != nil {
			slog.Error("failed to marshal event", "event_type", e.Type, "error", err)
			return
		}

		if e.WorkspaceID != "" {
			realtime.M.RecordEvent(e.Type)
			b.BroadcastToWorkspace(e.WorkspaceID, data)
		} else if strings.HasPrefix(e.Type, "daemon:") {
			realtime.M.RecordEvent(e.Type)
			b.Broadcast(data)
		}
	})
}
