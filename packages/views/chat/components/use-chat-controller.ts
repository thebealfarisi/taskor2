"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { useWorkspaceId } from "@multica/core/hooks";
import { api, dispatchReasonCode } from "@multica/core/api";
import {
  chatKeys,
  isTaskMessageTaskId,
  sortChatSessions,
} from "@multica/core/chat/queries";
import {
  useCreateChatSession,
  useMarkChatSessionRead,
  useSetChatSessionProject,
  useSetChatSessionArchived,
} from "@multica/core/chat/mutations";
import { useChatStore } from "@multica/core/chat";
import { upsertChatMessageToCaches } from "@multica/core/chat/message-cache";
import { useChatDraftRestore } from "./use-chat-draft-restore";
import { useChatTaskActions } from "./use-chat-task-actions";
import { createLogger } from "@multica/core/logger";
import type {
  Agent,
  Attachment,
  ChatMessage,
} from "@multica/core/types";
import { useT } from "../../i18n";
import { useAppForeground } from "../../common/use-app-foreground";
import {
  deriveChatTitle,
  isStillOnComposeTarget,
  planProjectContextChange,
  hasInFlightPendingTask,
  seedAcceptedPendingTask,
} from "./chat-controller-helpers";
import { useChatMessageFeed } from "./use-chat-message-feed";
import { useChatAgentContext } from "./use-chat-agent-context";

export {
  deriveChatTitle,
  isStillOnComposeTarget,
  type ProjectContextChange,
  planProjectContextChange,
  hasInFlightPendingTask,
  seedAcceptedPendingTask,
} from "./chat-controller-helpers";

const uiLogger = createLogger("chat.ui");
const apiLogger = createLogger("chat.api");

/**
 * Layout-agnostic chat controller. Holds every piece of chat conversation
 * state and behavior — agent resolution, session lookup, the await-then-render
 * send/stop/cancel flow, message pagination, and auto-mark-read — so that
 * both surfaces render the same conversation logic:
 *
 *  - ChatWindow: the floating FAB overlay (adds resize / expand / minimize).
 *  - ChatPage:   the first-class Chat tab (two-pane thread list + conversation).
 *
 * The only thing the caller supplies is `isActive` — whether its surface is
 * currently on screen — which gates auto-mark-read so a background overlay
 * doesn't silently clear unread state the user hasn't actually seen.
 */
export function useChatController(opts?: { isActive?: boolean }) {
  const isActive = opts?.isActive ?? true;
  const { t } = useT("chat");
  const wsId = useWorkspaceId();
  const activeSessionId = useChatStore((s) => s.activeSessionId);
  const setActiveSession = useChatStore((s) => s.setActiveSession);
  const setSelectedAgentId = useChatStore((s) => s.setSelectedAgentId);
  const setSelectedProjectId = useChatStore((s) => s.setSelectedProjectId);

  const agentContext = useChatAgentContext(wsId, activeSessionId);
  const messageFeed = useChatMessageFeed(activeSessionId);

  const {
    user,
    agents,
    availableAgents,
    agentsSettled,
    sessions,
    sessionsLoaded,
    projects,
    selectedAgentId,
    activeProjectId,
    projectContextUnsupported,
    currentSession,
    isSessionArchived,
    isAgentArchived,
    isAgentAccessRevoked,
    isAgentRuntimeBound,
    activeAgent,
    noAgent,
    availability,
  } = agentContext;

  const {
    messages,
    pendingTask,
    pendingTaskId,
    showSkeleton,
    hasMessages,
    firstItemIndex,
    hasOlderMessages,
    isFetchingOlderMessages,
    fetchOlderMessages,
  } = messageFeed;

  const stopRequestedBeforeTaskRef = useRef(false);
  const appForeground = useAppForeground();

  const { restoreDraftRequest, enqueueLocalRestore, handleRestoreDraftApplied } =
    useChatDraftRestore(activeSessionId, isActive && appForeground);

  const {
    cancelChatTask,
    handleEditQueuedTask,
    handleRemoveQueuedTask,
    handleClearQueuedTasks,
    handleSendQueuedTaskNow,
  } = useChatTaskActions(activeSessionId, enqueueLocalRestore);

  // Nonce handed to ChatInput to pull focus into the compose box when a new
  // chat starts. Bumped by handleNewChat / handleStartNewChat only, so
  // selecting an existing chat or a deep link never steals focus.
  const [focusInputRequest, setFocusInputRequest] = useState(0);
  const requestInputFocus = useCallback(
    () => setFocusInputRequest((n) => n + 1),
    [],
  );

  const qc = useQueryClient();
  const createSession = useCreateChatSession();
  const markRead = useMarkChatSessionRead();
  const setSessionProject = useSetChatSessionProject();
  const setArchived = useSetChatSessionArchived();

  // Auto mark-as-read whenever the user is actively looking at a session with
  // unread state.
  const currentHasUnread =
    sessions.find((s) => s.id === activeSessionId)?.has_unread ?? false;
  useEffect(() => {
    if (!isActive || !appForeground || !activeSessionId) return;
    if (!currentHasUnread) return;
    const sessionId = activeSessionId;
    const timer = setTimeout(() => {
      if (useChatStore.getState().activeSessionId !== sessionId) return;
      uiLogger.info("auto markRead", { sessionId });
      markRead.mutate(sessionId);
    }, 0);
    return () => clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- markRead ref stable
  }, [isActive, appForeground, activeSessionId, currentHasUnread]);

  const sessionPromiseRef = useRef<Promise<string | null> | null>(null);
  const ensureSession = useCallback(
    async (titleSeed: string): Promise<string | null> => {
      if (
        activeSessionId &&
        (!sessionsLoaded ||
          sessions.some((s) => s.id === activeSessionId) ||
          hasInFlightPendingTask(qc, activeSessionId))
      ) {
        return activeSessionId;
      }
      if (!activeAgent) return null;
      if (sessionPromiseRef.current) return sessionPromiseRef.current;

      const promise = (async () => {
        try {
          const session = await createSession.mutateAsync({
            agent_id: activeAgent.id,
            title: deriveChatTitle(titleSeed),
            project_id: activeProjectId,
          });
          return session.id;
        } finally {
          sessionPromiseRef.current = null;
        }
      })();
      sessionPromiseRef.current = promise;
      return promise;
    },
    [
      activeSessionId,
      activeAgent,
      activeProjectId,
      createSession,
      sessions,
      sessionsLoaded,
      qc,
    ],
  );

  // Self-heal a dangling `activeSessionId`.
  useEffect(() => {
    if (!activeSessionId || !sessionsLoaded) return;
    if (sessions.some((s) => s.id === activeSessionId)) return;
    if (hasInFlightPendingTask(qc, activeSessionId)) return;
    uiLogger.info("clearing dangling activeSessionId", { sessionId: activeSessionId });
    setActiveSession(null);
  }, [activeSessionId, sessionsLoaded, sessions, qc, setActiveSession]);

  const uploadEnabled = !!activeAgent;

  const handleSend = useCallback(
    async (
      content: string,
      attachmentIds?: string[],
      commitInput?: (options?: { extraDraftKeys?: string[]; clearEditor?: boolean }) => void,
      draftAttachments: Attachment[] = [],
    ): Promise<boolean> => {
      if (!activeAgent) {
        apiLogger.warn("sendChatMessage skipped: no active agent");
        return false;
      }
      if (isAgentArchived) {
        apiLogger.warn("sendChatMessage skipped: agent is archived", {
          sessionId: activeSessionId,
          agentId: activeAgent.id,
        });
        return false;
      }
      if (isAgentAccessRevoked) {
        apiLogger.warn("sendChatMessage skipped: invoke permission revoked", {
          sessionId: activeSessionId,
          agentId: activeAgent.id,
        });
        return false;
      }
      if (pendingTaskId && pendingTask?.supports_queue !== true) {
        apiLogger.warn("sendChatMessage skipped: server does not support follow-up queues", {
          sessionId: activeSessionId,
        });
        return false;
      }
      if (!isAgentRuntimeBound) {
        toast.error(t(($) => $.input.runtime_required_toast));
        return false;
      }

      const finalContent = content;
      const isNewSession = !activeSessionId;

      apiLogger.info("sendChatMessage.start", {
        sessionId: activeSessionId,
        isNewSession,
        agentId: activeAgent.id,
        contentLength: finalContent.length,
        attachmentCount: attachmentIds?.length ?? 0,
      });

      let sessionId: string | null = null;
      try {
        sessionId = await ensureSession(finalContent);
      } catch (err) {
        apiLogger.error("sendChatMessage.ensureSession.error", err);
        const reason = dispatchReasonCode(err);
        toast.error(
          reason === "invocation_not_allowed"
            ? t(($) => $.input.send_blocked_toast)
            : reason === "agent_runtime_required"
              ? t(($) => $.input.runtime_required_toast)
              : t(($) => $.input.send_failed_toast),
        );
        return false;
      }
      if (!sessionId) {
        apiLogger.warn("sendChatMessage aborted: ensureSession returned null");
        return false;
      }

      let result;
      try {
        result = await api.sendChatMessage(sessionId, finalContent, attachmentIds);
      } catch (err) {
        apiLogger.error("sendChatMessage.error", { sessionId, err });
        const reason = dispatchReasonCode(err);
        toast.error(
          reason === "invocation_not_allowed"
            ? t(($) => $.input.send_blocked_toast)
            : reason === "agent_runtime_required"
              ? t(($) => $.input.runtime_required_toast)
              : t(($) => $.input.send_failed_toast),
        );
        return false;
      }
      apiLogger.info("sendChatMessage.success", {
        sessionId,
        messageId: result.message_id,
        taskId: result.task_id,
      });

      const sent: ChatMessage = {
        id: result.message_id,
        chat_session_id: sessionId,
        role: "user",
        content: finalContent,
        task_id: result.task_id,
        created_at: result.created_at,
        attachments: draftAttachments,
      };
      upsertChatMessageToCaches(qc, sessionId, sent, { seedIfMissing: true });
      seedAcceptedPendingTask(qc, sessionId, {
        task_id: result.task_id,
        created_at: result.created_at,
        message_id: result.message_id,
        content: finalContent,
        supports_queue: result.supports_queue,
        queued: result.queued,
      });

      const live = useChatStore.getState();
      const stillOnSourceSession = isStillOnComposeTarget(live.activeSessionId, activeSessionId);
      if (stillOnSourceSession) {
        setActiveSession(sessionId);
      }
      commitInput?.({ extraDraftKeys: [sessionId], clearEditor: stillOnSourceSession });

      if (stopRequestedBeforeTaskRef.current) {
        stopRequestedBeforeTaskRef.current = false;
        await cancelChatTask(result.task_id, sessionId, {
          restoreDraftToInput: true,
          source: "deferred-send",
        });
        return false;
      }
      if (attachmentIds && attachmentIds.length > 0 && result.attachment_ids) {
        const boundIds = new Set(result.attachment_ids);
        const missing = attachmentIds.filter((id) => !boundIds.has(id));
        if (missing.length > 0) {
          apiLogger.warn("sendChatMessage.attachments missing after send", {
            sessionId,
            messageId: result.message_id,
            missing,
          });
          toast.error(t(($) => $.input.attachment_bind_failed_toast));
        }
      }
      qc.invalidateQueries({ queryKey: chatKeys.messages(sessionId) });
      qc.invalidateQueries({ queryKey: chatKeys.messagesPage(sessionId) });
      return true;
    },
    [
      activeSessionId,
      activeAgent,
      isAgentArchived,
      isAgentAccessRevoked,
      pendingTask,
      pendingTaskId,
      isAgentRuntimeBound,
      ensureSession,
      cancelChatTask,
      qc,
      setActiveSession,
      t,
    ],
  );

  const handleStop = useCallback(() => {
    if (!pendingTaskId || !activeSessionId) {
      apiLogger.debug("cancelTask skipped: no pending task");
      return;
    }
    if (!isTaskMessageTaskId(pendingTaskId)) {
      stopRequestedBeforeTaskRef.current = true;
      apiLogger.info("cancelTask.deferred until server task id", {
        taskId: pendingTaskId,
        sessionId: activeSessionId,
      });
      return;
    }
    cancelChatTask(pendingTaskId, activeSessionId, {
      restoreDraftToInput: true,
      source: "active-input",
    });
  }, [pendingTaskId, activeSessionId, cancelChatTask]);

  const handleNewChat = useCallback(() => {
    uiLogger.info("newChat", {
      previousSessionId: activeSessionId,
      previousPendingTask: pendingTaskId,
    });
    setSelectedProjectId(null);
    setActiveSession(null);
    requestInputFocus();
  }, [
    activeSessionId,
    pendingTaskId,
    setSelectedProjectId,
    setActiveSession,
    requestInputFocus,
  ]);

  const handleStartNewChat = useCallback(
    (agent: Agent) => {
      uiLogger.info("startNewChat", {
        agentId: agent.id,
        previousSessionId: activeSessionId,
      });
      setSelectedAgentId(agent.id);
      setSelectedProjectId(null);
      setActiveSession(null);
      requestInputFocus();
    },
    [
      activeSessionId,
      setSelectedAgentId,
      setSelectedProjectId,
      setActiveSession,
      requestInputFocus,
    ],
  );

  const handleSelectSession = useCallback(
    (session: { id: string; agent_id: string; project_id?: string | null }) => {
      if (activeAgent && session.agent_id !== activeAgent.id) {
        uiLogger.info("selectSession (cross-agent)", {
          from: activeAgent.id,
          toAgent: session.agent_id,
          toSession: session.id,
        });
        setSelectedAgentId(session.agent_id);
      }
      setActiveSession(session.id);
    },
    [activeAgent, setSelectedAgentId, setActiveSession],
  );

  const handleProjectChange = useCallback(
    (projectId: string | null) => {
      if (projectId === activeProjectId) return;
      uiLogger.info("selectProjectContext", {
        from: activeProjectId,
        to: projectId,
        previousSessionId: activeSessionId,
      });
      const plan = planProjectContextChange({
        targetProjectId: projectId,
        activeSessionId,
        currentSession: currentSession ?? null,
      });
      switch (plan.kind) {
        case "awaitSession":
          return;
        case "detachCurrent":
          setSessionProject.mutate({ sessionId: plan.sessionId, projectId: null });
          break;
        case "startFreshChat":
          setSelectedAgentId(plan.agentId);
          setSelectedProjectId(plan.projectId);
          setActiveSession(null);
          break;
        case "setDraftProject":
          setSelectedProjectId(plan.projectId);
          break;
      }
      requestInputFocus();
    }, [
      activeProjectId,
      activeSessionId,
      currentSession,
      setSessionProject,
      setSelectedAgentId,
      setSelectedProjectId,
      setActiveSession,
      requestInputFocus,
    ],
  );

  const advanceSelectionAfterArchive = useCallback(
    (session: { id: string; agent_id: string }) => {
      if (activeSessionId !== session.id) return;
      const history = sortChatSessions(
        sessions.filter((s) => s.status !== "archived"),
      );
      const idx = history.findIndex((s) => s.id === session.id);
      const next = history[idx + 1] ?? history[idx - 1] ?? null;
      if (next) handleSelectSession(next);
      else setActiveSession(null);
    },
    [activeSessionId, sessions, handleSelectSession, setActiveSession],
  );

  const archiveSession = useCallback(
    (sessionId: string) => setArchived.mutate({ sessionId, archived: true }),
    [setArchived],
  );

  return {
    // identity / lists
    wsId,
    user,
    agents,
    availableAgents,
    agentsSettled,
    sessions,
    projects,
    activeSessionId,
    selectedAgentId,
    activeProjectId,
    projectContextUnsupported,
    isProjectUpdating:
      setSessionProject.isPending || (!!activeSessionId && !currentSession),
    currentSession,
    isSessionArchived,
    isAgentArchived,
    isAgentAccessRevoked,
    isAgentRuntimeBound,
    activeAgent,
    noAgent,
    availability,
    // messages
    messages,
    pendingTask,
    pendingTaskId,
    showSkeleton,
    hasMessages,
    firstItemIndex,
    hasOlderMessages,
    isFetchingOlderMessages,
    fetchOlderMessages,
    // draft restore
    restoreDraftRequest,
    handleRestoreDraftApplied,
    // compose-box focus nonce (bumped on new chat)
    focusInputRequest,
    // actions
    handleSend,
    handleStop,
    handleSendQueuedTaskNow,
    handleEditQueuedTask,
    handleRemoveQueuedTask,
    handleClearQueuedTasks,
    uploadEnabled,
    handleNewChat,
    handleStartNewChat,
    handleSelectSession,
    handleProjectChange,
    advanceSelectionAfterArchive,
    archiveSession,
    // store setters (for surfaces that sync selection to the URL, etc.)
    setActiveSession,
    setSelectedAgentId,
  };
}

export type ChatController = ReturnType<typeof useChatController>;
