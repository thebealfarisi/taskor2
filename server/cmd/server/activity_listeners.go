package main

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// registerActivityListeners wires up event bus listeners that record activity
// entries in the activity_log table. Each listener creates one or more activity
// records depending on what changed, then publishes an activity:created event
// for WS broadcasting.
func registerActivityListeners(bus *events.Bus, queries *db.Queries) {
	ctx := context.Background()

	bus.Subscribe(protocol.EventIssueCreated, func(e events.Event) {
		handleIssueCreatedActivity(ctx, bus, queries, e)
	})

	bus.Subscribe(protocol.EventIssueUpdated, func(e events.Event) {
		handleIssueUpdatedActivity(ctx, bus, queries, e)
	})

	bus.Subscribe(protocol.EventTaskCompleted, func(e events.Event) {
		handleTaskActivity(ctx, bus, queries, e, "task_completed")
	})

	bus.Subscribe(protocol.EventTaskFailed, func(e events.Event) {
		handleTaskActivity(ctx, bus, queries, e, "task_failed")
	})
}

func handleIssueCreatedActivity(ctx context.Context, bus *events.Bus, queries *db.Queries, e events.Event) {
	payload, ok := e.Payload.(map[string]any)
	if !ok {
		return
	}
	issue, ok := payload["issue"].(handler.IssueResponse)
	if !ok {
		return
	}

	recordActivity(ctx, bus, queries, e, issue.WorkspaceID, issue.ID, "created", []byte("{}"))
}

func handleIssueUpdatedActivity(ctx context.Context, bus *events.Bus, queries *db.Queries, e events.Event) {
	payload, ok := e.Payload.(map[string]any)
	if !ok {
		return
	}
	issue, ok := payload["issue"].(handler.IssueResponse)
	if !ok {
		return
	}

	recordStatusChangeActivity(ctx, bus, queries, e, issue, payload)
	recordPriorityChangeActivity(ctx, bus, queries, e, issue, payload)
	recordAssigneeChangeActivity(ctx, bus, queries, e, issue, payload)
	recordDateChangeActivity(ctx, bus, queries, e, issue, payload, "start_date_changed", "prev_start_date", issue.StartDate, "start_date_changed")
	recordDateChangeActivity(ctx, bus, queries, e, issue, payload, "due_date_changed", "prev_due_date", issue.DueDate, "due_date_changed")
	recordTitleChangeActivity(ctx, bus, queries, e, issue, payload)
	recordDescriptionChangeActivity(ctx, bus, queries, e, issue, payload)
}

func recordStatusChangeActivity(ctx context.Context, bus *events.Bus, queries *db.Queries, e events.Event, issue handler.IssueResponse, payload map[string]any) {
	if statusChanged, _ := payload["status_changed"].(bool); !statusChanged {
		return
	}
	prevStatus, _ := payload["prev_status"].(string)
	details, _ := json.Marshal(map[string]string{
		"from": prevStatus,
		"to":   issue.Status,
	})
	recordActivity(ctx, bus, queries, e, issue.WorkspaceID, issue.ID, "status_changed", details)
}

func recordPriorityChangeActivity(ctx context.Context, bus *events.Bus, queries *db.Queries, e events.Event, issue handler.IssueResponse, payload map[string]any) {
	if priorityChanged, _ := payload["priority_changed"].(bool); !priorityChanged {
		return
	}
	prevPriority, _ := payload["prev_priority"].(string)
	details, _ := json.Marshal(map[string]string{
		"from": prevPriority,
		"to":   issue.Priority,
	})
	recordActivity(ctx, bus, queries, e, issue.WorkspaceID, issue.ID, "priority_changed", details)
}

func recordAssigneeChangeActivity(ctx context.Context, bus *events.Bus, queries *db.Queries, e events.Event, issue handler.IssueResponse, payload map[string]any) {
	if assigneeChanged, _ := payload["assignee_changed"].(bool); !assigneeChanged {
		return
	}
	prevAssigneeType, _ := payload["prev_assignee_type"].(*string)
	prevAssigneeID, _ := payload["prev_assignee_id"].(*string)

	detailsMap := map[string]string{}
	if prevAssigneeType != nil {
		detailsMap["from_type"] = *prevAssigneeType
	}
	if prevAssigneeID != nil {
		detailsMap["from_id"] = *prevAssigneeID
	}
	if issue.AssigneeType != nil {
		detailsMap["to_type"] = *issue.AssigneeType
	}
	if issue.AssigneeID != nil {
		detailsMap["to_id"] = *issue.AssigneeID
	}

	details, _ := json.Marshal(detailsMap)
	recordActivity(ctx, bus, queries, e, issue.WorkspaceID, issue.ID, "assignee_changed", details)
}

func recordDateChangeActivity(ctx context.Context, bus *events.Bus, queries *db.Queries, e events.Event, issue handler.IssueResponse, payload map[string]any, flagKey, prevKey string, newDate *string, action string) {
	if changed, _ := payload[flagKey].(bool); !changed {
		return
	}
	prevDate := ""
	if v, ok := payload[prevKey].(*string); ok && v != nil {
		prevDate = *v
	}
	newDateStr := ""
	if newDate != nil {
		newDateStr = *newDate
	}
	details, _ := json.Marshal(map[string]string{
		"from": prevDate,
		"to":   newDateStr,
	})
	recordActivity(ctx, bus, queries, e, issue.WorkspaceID, issue.ID, action, details)
}

func recordTitleChangeActivity(ctx context.Context, bus *events.Bus, queries *db.Queries, e events.Event, issue handler.IssueResponse, payload map[string]any) {
	if titleChanged, _ := payload["title_changed"].(bool); !titleChanged {
		return
	}
	prevTitle, _ := payload["prev_title"].(string)
	details, _ := json.Marshal(map[string]string{
		"from": prevTitle,
		"to":   issue.Title,
	})
	recordActivity(ctx, bus, queries, e, issue.WorkspaceID, issue.ID, "title_changed", details)
}

func recordDescriptionChangeActivity(ctx context.Context, bus *events.Bus, queries *db.Queries, e events.Event, issue handler.IssueResponse, payload map[string]any) {
	if descriptionChanged, _ := payload["description_changed"].(bool); !descriptionChanged {
		return
	}
	recordActivity(ctx, bus, queries, e, issue.WorkspaceID, issue.ID, "description_updated", []byte("{}"))
}

func recordActivity(ctx context.Context, bus *events.Bus, queries *db.Queries, e events.Event, workspaceID, issueID, action string, details []byte) {
	activity, err := queries.CreateActivity(ctx, db.CreateActivityParams{
		ID:          dbid.NewV7(),
		WorkspaceID: parseUUID(workspaceID),
		IssueID:     parseUUID(issueID),
		ActorType:   util.StrToText(e.ActorType),
		ActorID:     optionalUUID(e.ActorID),
		Action:      action,
		Details:     details,
	})
	if err != nil {
		slog.Error("activity: failed to record activity",
			"issue_id", issueID, "action", action, "error", err)
		return
	}

	publishActivityEvent(bus, e, activity)
}

// handleTaskActivity records an activity for task:completed or task:failed events.
func handleTaskActivity(ctx context.Context, bus *events.Bus, queries *db.Queries, e events.Event, action string) {
	payload, ok := e.Payload.(map[string]any)
	if !ok {
		return
	}
	agentID, _ := payload["agent_id"].(string)
	issueID, _ := payload["issue_id"].(string)
	if issueID == "" {
		return
	}

	// Look up issue to get workspace_id
	issue, err := queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		slog.Error("activity: failed to get issue for task event",
			"issue_id", issueID, "action", action, "error", err)
		return
	}

	activity, err := queries.CreateActivity(ctx, db.CreateActivityParams{
		ID:          dbid.NewV7(),
		WorkspaceID: issue.WorkspaceID,
		IssueID:     parseUUID(issueID),
		ActorType:   util.StrToText("agent"),
		ActorID:     parseUUID(agentID),
		Action:      action,
		Details:     []byte("{}"),
	})
	if err != nil {
		slog.Error("activity: failed to record task activity",
			"issue_id", issueID, "action", action, "error", err)
		return
	}

	publishActivityEvent(bus, e, activity)
}

// publishActivityEvent sends an activity:created event for WS broadcasting.
// Payload matches frontend ActivityCreatedPayload: { issue_id, entry: TimelineEntry }
func publishActivityEvent(bus *events.Bus, original events.Event, activity db.ActivityLog) {
	actorType := ""
	if activity.ActorType.Valid {
		actorType = activity.ActorType.String
	}
	action := activity.Action
	bus.Publish(events.Event{
		Type:        protocol.EventActivityCreated,
		WorkspaceID: original.WorkspaceID,
		ActorType:   original.ActorType,
		ActorID:     original.ActorID,
		Payload: map[string]any{
			"issue_id": util.UUIDToString(activity.IssueID),
			"entry": map[string]any{
				"type":       "activity",
				"id":         util.UUIDToString(activity.ID),
				"actor_type": actorType,
				"actor_id":   util.UUIDToString(activity.ActorID),
				"action":     action,
				"details":    json.RawMessage(activity.Details),
				"created_at": util.TimestampToString(activity.CreatedAt),
			},
		},
	})
}
