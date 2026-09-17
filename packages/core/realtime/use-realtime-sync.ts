"use client";

import { useEffect, useRef } from "react";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import type { WSClient } from "../api/ws-client";
import type { StoreApi, UseBoundStore } from "zustand";
import type { AuthState } from "../auth/store";
import { createLogger } from "../logger";
import { getCurrentWsId } from "../platform/workspace-storage";
import { issueKeys } from "../issues/queries";
import { projectKeys } from "../projects/queries";
import { pinKeys } from "../pins/queries";
import { autopilotKeys } from "../autopilots/queries";
import { runtimeKeys } from "../runtimes/queries";
import { issueStatusKeys } from "../issue-statuses/queries";
import {
  agentTaskSnapshotKeys,
  workspaceWorkingAgentsKeys,
  agentActivityKeys,
  agentRunCountsKeys,
  agentTasksKeys,
} from "../agents/queries";
import { githubKeys } from "../github/queries";
import { larkKeys } from "../lark/queries";
import { slackKeys } from "../slack/queries";
import { dingtalkKeys } from "../dingtalk/queries";
import { wecomKeys } from "../wecom/queries";
import { telegramKeys } from "../telegram/queries";
import { onInboxInvalidate, onInboxSummaryInvalidate } from "../inbox/ws-updaters";
import { workspaceKeys } from "../workspace/queries";
import { useHasOnboarded } from "../paths";
import {
  invalidateWorkspaceScopedQueries,
  invalidateSquadMemberStatusQueries,
} from "./workspace-sync";
import { registerIssueListeners } from "./listeners/issue-listeners";
import { registerCommentListeners } from "./listeners/comment-listeners";
import { registerWorkspaceListeners } from "./listeners/workspace-listeners";
import { registerChatListeners } from "./listeners/chat-listeners";

// Re-export cache helpers and synchronization functions for backward compatibility & tests
export {
  TASK_MESSAGE_FLUSH_MS,
  invalidateChatMessageQueries,
  refetchPendingChatAggregate,
  applyChatMessageToCache,
  applyChatDoneToCache,
  applyChatQuickActionsToCache,
  applyChatSessionUpdatedToCache,
  applyChatCancelFinalizedToCache,
  removeChatMessageFromPageCache,
  removeChatMessageFromCaches,
  type ChatSessionUpdatedPayload,
} from "./chat-sync";
export { resolveInboxSourceSlug, handleInboxNew } from "./inbox-sync";
export {
  applyWorkspaceUpdatedToCache,
  invalidateWorkspaceScopedQueries,
  invalidateSquadMemberStatusQueries,
  relocateAfterWorkspaceLoss,
} from "./workspace-sync";

const logger = createLogger("realtime-sync");

// Event types handled by specific domain listeners -- skip generic refresh
const specificEvents = new Set([
  "workspace:updated",
  "issue:updated",
  "issue:created",
  "issue:deleted",
  "issue_attachments:changed",
  "issue_labels:changed",
  "issue_metadata:changed",
  "issue_properties:changed",
  "property:created",
  "property:updated",
  "inbox:new",
  "comment:created",
  "comment:updated",
  "comment:deleted",
  "comment:resolved",
  "comment:unresolved",
  "activity:created",
  "reaction:added",
  "reaction:removed",
  "issue_reaction:added",
  "issue_reaction:removed",
  "subscriber:added",
  "subscriber:removed",
  "daemon:heartbeat",
  "chat:message",
  "chat:done",
  "chat:quick_actions",
  "chat:cancel_finalized",
  "chat:session_read",
  "chat:session_deleted",
  "chat:session_updated",
  "task:message",
]);

function createRealtimeRefreshMap(
  qc: QueryClient,
  authStore: UseBoundStore<StoreApi<AuthState>>,
): Record<string, () => void> {
  return {
    inbox: () => {
      const wsId = getCurrentWsId();
      if (wsId) onInboxInvalidate(qc, wsId);
      onInboxSummaryInvalidate(qc);
    },
    agent: () => {
      const wsId = getCurrentWsId();
      if (wsId) {
        qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
        qc.invalidateQueries({ queryKey: workspaceWorkingAgentsKeys.all(wsId) });
        invalidateSquadMemberStatusQueries(qc, wsId);
      }
    },
    member: () => {
      const wsId = getCurrentWsId();
      if (wsId) qc.invalidateQueries({ queryKey: workspaceKeys.members(wsId) });
    },
    workspace: () => {
      qc.invalidateQueries({ queryKey: workspaceKeys.list() });
    },
    skill: () => {
      const wsId = getCurrentWsId();
      if (wsId) qc.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) });
    },
    project: () => {
      const wsId = getCurrentWsId();
      if (wsId) qc.invalidateQueries({ queryKey: projectKeys.all(wsId) });
    },
    squad: () => {
      const wsId = getCurrentWsId();
      if (wsId) {
        qc.invalidateQueries({ queryKey: workspaceKeys.squads(wsId) });
        qc.invalidateQueries({ queryKey: issueKeys.all(wsId) });
      }
    },
    label: () => {
      const wsId = getCurrentWsId();
      if (wsId) {
        qc.invalidateQueries({ queryKey: ["labels", wsId] });
        qc.invalidateQueries({ queryKey: issueKeys.all(wsId) });
        qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
        qc.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) });
      }
    },
    issue_status: () => {
      const wsId = getCurrentWsId();
      if (wsId) qc.invalidateQueries({ queryKey: issueStatusKeys.all(wsId) });
    },
    pin: () => {
      const wsId = getCurrentWsId();
      const userId = authStore.getState().user?.id;
      if (wsId && userId) qc.invalidateQueries({ queryKey: pinKeys.all(wsId, userId) });
    },
    daemon: () => {
      const wsId = getCurrentWsId();
      if (wsId) {
        qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
        invalidateSquadMemberStatusQueries(qc, wsId);
      }
    },
    autopilot: () => {
      const wsId = getCurrentWsId();
      if (wsId) qc.invalidateQueries({ queryKey: autopilotKeys.all(wsId) });
    },
    github_installation: () => {
      const wsId = getCurrentWsId();
      if (wsId) qc.invalidateQueries({ queryKey: githubKeys.installations(wsId) });
    },
    lark_installation: () => {
      const wsId = getCurrentWsId();
      if (wsId) qc.invalidateQueries({ queryKey: larkKeys.installations(wsId) });
    },
    slack_installation: () => {
      const wsId = getCurrentWsId();
      if (wsId) qc.invalidateQueries({ queryKey: slackKeys.installations(wsId) });
    },
    dingtalk_installation: () => {
      const wsId = getCurrentWsId();
      if (wsId) qc.invalidateQueries({ queryKey: dingtalkKeys.installations(wsId) });
    },
    dingtalk_group_route: () => {
      const wsId = getCurrentWsId();
      if (wsId) qc.invalidateQueries({ queryKey: dingtalkKeys.groupRoutes(wsId) });
    },
    vcs_connection: () => {
      const wsId = getCurrentWsId();
      if (wsId) qc.invalidateQueries({ queryKey: ["vcs", wsId] });
    },
    wecom_installation: () => {
      const wsId = getCurrentWsId();
      if (wsId) qc.invalidateQueries({ queryKey: wecomKeys.installations(wsId) });
    },
    telegram_installation: () => {
      const wsId = getCurrentWsId();
      if (wsId) qc.invalidateQueries({ queryKey: telegramKeys.installations(wsId) });
    },
    pull_request: () => {
      qc.invalidateQueries({ queryKey: ["github", "pull-requests"] });
    },
    task: () => {
      const wsId = getCurrentWsId();
      if (!wsId) return;
      qc.invalidateQueries({ queryKey: agentTaskSnapshotKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: workspaceWorkingAgentsKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: issueKeys.tableAll(wsId) });
      qc.invalidateQueries({ queryKey: agentActivityKeys.last30d(wsId) });
      qc.invalidateQueries({ queryKey: agentRunCountsKeys.last30d(wsId) });
      qc.invalidateQueries({ queryKey: agentTasksKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: ["issues", "tasks"] });
      qc.invalidateQueries({ queryKey: ["issues", "usage"] });
      invalidateSquadMemberStatusQueries(qc, wsId);
      qc.invalidateQueries({ queryKey: issueKeys.commentTriggerPreviewAll() });
    },
  };
}

export interface RealtimeSyncStores {
  authStore: UseBoundStore<StoreApi<AuthState>>;
}

/**
 * Centralized WS -> store sync. Called once from WSProvider.
 *
 * Uses the "WS as invalidation signal + refetch" pattern:
 * - onAny handler extracts event prefix and calls the matching store refresh
 * - Debounce per-prefix prevents rapid-fire refetches (e.g. bulk issue updates)
 * - Precise handlers only for side effects (toast, navigation, self-check)
 */
export function useRealtimeSync(
  ws: WSClient | null,
  stores: RealtimeSyncStores,
  onToast?: (message: string, type?: "info" | "error") => void,
) {
  const { authStore } = stores;
  const qc = useQueryClient();

  const hasOnboarded = useHasOnboarded();
  const hasOnboardedRef = useRef(hasOnboarded);
  hasOnboardedRef.current = hasOnboarded;

  // Main sync: onAny -> refreshMap with debounce + modular domain listeners
  useEffect(() => {
    if (!ws) return;

    const refreshMap = createRealtimeRefreshMap(qc, authStore);
    const timers = new Map<string, ReturnType<typeof setTimeout>>();
    const debouncedRefresh = (prefix: string, fn: () => void) => {
      const existing = timers.get(prefix);
      if (existing) clearTimeout(existing);
      timers.set(
        prefix,
        setTimeout(() => {
          timers.delete(prefix);
          fn();
        }, 100),
      );
    };

    const unsubAny = ws.onAny((msg) => {
      if (specificEvents.has(msg.type)) return;
      const prefix = msg.type.split(":")[0] ?? "";
      const refresh = refreshMap[prefix];
      if (refresh) debouncedRefresh(prefix, refresh);
    });

    const unsubIssues = registerIssueListeners(ws, qc);
    const unsubComments = registerCommentListeners(ws, qc);
    const unsubWorkspaces = registerWorkspaceListeners(
      ws,
      qc,
      authStore,
      hasOnboardedRef,
      onToast,
    );
    const unsubChat = registerChatListeners(
      ws,
      qc,
      () => authStore.getState().user?.id,
    );

    return () => {
      unsubAny();
      unsubIssues();
      unsubComments();
      unsubWorkspaces();
      unsubChat();
      timers.forEach(clearTimeout);
      timers.clear();
    };
  }, [ws, qc, authStore, onToast]);

  // Reconnect -> refetch all data to recover missed events
  useEffect(() => {
    if (!ws) return;

    const unsub = ws.onReconnect(async () => {
      logger.info("reconnected, refetching all data");
      try {
        invalidateWorkspaceScopedQueries(qc);
      } catch (e) {
        logger.error("reconnect refetch failed", e);
      }
    });

    return unsub;
  }, [ws, qc]);

  // New WSClient instance (workspace switch) -> invalidate workspace-scoped
  // queries to recover events missed while the previous instance was torn down.
  const wsInstanceRef = useRef<WSClient | null>(null);
  useEffect(() => {
    if (!ws) return;
    if (wsInstanceRef.current === null) {
      wsInstanceRef.current = ws;
      return;
    }
    if (wsInstanceRef.current === ws) return;
    wsInstanceRef.current = ws;

    logger.info("new WSClient instance detected, invalidating workspace queries");
    invalidateWorkspaceScopedQueries(qc);
  }, [ws, qc]);
}
