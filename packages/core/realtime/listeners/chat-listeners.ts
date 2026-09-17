import type { QueryClient } from "@tanstack/react-query";
import type { WSClient } from "../../api/ws-client";
import { createLogger } from "../../logger";
import { getCurrentWsId } from "../../platform/workspace-storage";
import {
  chatKeys,
  isTaskMessageTimelineHeld,
  mergeTaskMessagesBySeq,
} from "../../chat/queries";
import { useChatStore } from "../../chat";
import {
  promotePendingChatTask,
  removePendingChatTask,
} from "../../chat/pending";
import {
  applyChatCancelFinalizedToCache,
  applyChatDoneToCache,
  applyChatMessageToCache,
  applyChatQuickActionsToCache,
  applyChatSessionUpdatedToCache,
  invalidateChatMessageQueries,
  refetchPendingChatAggregate,
  TASK_MESSAGE_FLUSH_MS,
  type ChatSessionUpdatedPayload,
} from "../chat-sync";
import type {
  ChatCancelFinalizedPayload,
  ChatDonePayload,
  ChatMessageEventPayload,
  ChatPendingTask,
  ChatQuickActionsPayload,
  TaskCancelledPayload,
  TaskCompletedPayload,
  TaskDispatchPayload,
  TaskFailedPayload,
  TaskMessagePayload,
  TaskQueuedPayload,
  TaskRunningPayload,
  TaskWaitingLocalDirectoryPayload,
} from "../../types";

const chatWsLogger = createLogger("chat.ws");

export function registerChatListeners(
  ws: WSClient,
  qc: QueryClient,
  getCurrentUserId: () => string | undefined,
): () => void {
  const unsubs: (() => void)[] = [];

  const taskMessageBatches = new Map<string, TaskMessagePayload[]>();
  let taskMessageFlushTimer: ReturnType<typeof setTimeout> | null = null;

  const flushTaskMessages = () => {
    taskMessageFlushTimer = null;

    for (const [taskId, batch] of taskMessageBatches) {
      if (!isTaskMessageTimelineHeld(qc, taskId)) {
        continue;
      }
      qc.setQueryData<TaskMessagePayload[]>(
        chatKeys.taskMessages(taskId),
        (old = []) => mergeTaskMessagesBySeq(old, batch),
      );
    }
    taskMessageBatches.clear();
  };

  unsubs.push(
    ws.on("task:message", (p) => {
      const payload = p as TaskMessagePayload;
      if (!isTaskMessageTimelineHeld(qc, payload.task_id)) return;

      const batch = taskMessageBatches.get(payload.task_id);
      if (batch) batch.push(payload);
      else taskMessageBatches.set(payload.task_id, [payload]);

      if (!taskMessageFlushTimer) {
        taskMessageFlushTimer = setTimeout(flushTaskMessages, TASK_MESSAGE_FLUSH_MS);
      }

      chatWsLogger.debug("task:message (global)", {
        task_id: payload.task_id,
        seq: payload.seq,
        type: payload.type,
      });
    }),
  );

  let aggregateRefreshTimer: ReturnType<typeof setTimeout> | null = null;
  const invalidatePendingAggregate = () => {
    if (aggregateRefreshTimer) clearTimeout(aggregateRefreshTimer);
    aggregateRefreshTimer = setTimeout(() => {
      aggregateRefreshTimer = null;
      refetchPendingChatAggregate(qc, getCurrentWsId());
    }, 750);
  };
  const invalidateSessionLists = () => {
    const id = getCurrentWsId();
    if (id) qc.invalidateQueries({ queryKey: chatKeys.sessions(id) });
  };

  unsubs.push(
    ws.on("chat:message", (p) => {
      const payload = p as ChatMessageEventPayload;
      chatWsLogger.info("chat:message (global)", {
        chat_session_id: payload.chat_session_id,
        role: payload.role,
      });
      applyChatMessageToCache(qc, payload);
    }),
  );

  unsubs.push(
    ws.on("chat:done", (p) => {
      const payload = p as ChatDonePayload;
      chatWsLogger.info("chat:done (global)", {
        chat_session_id: payload.chat_session_id,
        task_id: payload.task_id,
      });
      applyChatDoneToCache(qc, payload);
      invalidatePendingAggregate();
      invalidateSessionLists();
    }),
  );

  unsubs.push(
    ws.on("chat:quick_actions", (p) => {
      const payload = p as ChatQuickActionsPayload;
      chatWsLogger.info("chat:quick_actions (global)", {
        chat_session_id: payload.chat_session_id,
        message_id: payload.message_id,
        count: payload.quick_actions?.length ?? 0,
      });
      void applyChatQuickActionsToCache(qc, payload);
    }),
  );

  unsubs.push(
    ws.on("chat:cancel_finalized", (p) => {
      const payload = p as ChatCancelFinalizedPayload;
      chatWsLogger.info("chat:cancel_finalized (global)", {
        chat_session_id: payload.chat_session_id,
        task_id: payload.task_id,
        outcome: payload.outcome,
      });
      applyChatCancelFinalizedToCache(qc, payload, getCurrentUserId());
      invalidatePendingAggregate();
      if (payload.outcome === "stopped") {
        invalidateSessionLists();
      }
    }),
  );

  unsubs.push(
    ws.on("task:queued", (p) => {
      const payload = p as TaskQueuedPayload;
      if (!payload.chat_session_id) return;
      qc.invalidateQueries({ queryKey: chatKeys.pendingTask(payload.chat_session_id) });
      invalidatePendingAggregate();
    }),
  );

  unsubs.push(
    ws.on("task:dispatch", (p) => {
      const payload = p as TaskDispatchPayload;
      if (!payload.chat_session_id) return;
      qc.setQueryData<ChatPendingTask>(
        chatKeys.pendingTask(payload.chat_session_id),
        (old) => promotePendingChatTask(old, payload.task_id, "running"),
      );
      invalidateChatMessageQueries(qc, payload.chat_session_id);
      qc.invalidateQueries({ queryKey: chatKeys.pendingTask(payload.chat_session_id) });
      invalidatePendingAggregate();
    }),
  );

  unsubs.push(
    ws.on("task:running", (p) => {
      const payload = p as TaskRunningPayload;
      if (!payload.chat_session_id) return;
      qc.setQueryData<ChatPendingTask>(
        chatKeys.pendingTask(payload.chat_session_id),
        (old) => promotePendingChatTask(old, payload.task_id, "running"),
      );
      invalidateChatMessageQueries(qc, payload.chat_session_id);
      qc.invalidateQueries({ queryKey: chatKeys.pendingTask(payload.chat_session_id) });
      invalidatePendingAggregate();
    }),
  );

  unsubs.push(
    ws.on("task:waiting_local_directory", (p) => {
      const payload = p as TaskWaitingLocalDirectoryPayload;
      if (!payload.chat_session_id) return;
      qc.setQueryData<ChatPendingTask>(
        chatKeys.pendingTask(payload.chat_session_id),
        (old) =>
          promotePendingChatTask(
            old,
            payload.task_id,
            "waiting_local_directory",
          ),
      );
      invalidateChatMessageQueries(qc, payload.chat_session_id);
      qc.invalidateQueries({ queryKey: chatKeys.pendingTask(payload.chat_session_id) });
      invalidatePendingAggregate();
    }),
  );

  unsubs.push(
    ws.on("task:cancelled", (p) => {
      const payload = p as TaskCancelledPayload;
      if (!payload.chat_session_id) return;
      chatWsLogger.info("task:cancelled (global, chat)", {
        task_id: payload.task_id,
        chat_session_id: payload.chat_session_id,
      });
      qc.setQueryData<ChatPendingTask>(
        chatKeys.pendingTask(payload.chat_session_id),
        (old) => removePendingChatTask(old, payload.task_id),
      );
      qc.invalidateQueries({ queryKey: chatKeys.pendingTask(payload.chat_session_id) });
      invalidateChatMessageQueries(qc, payload.chat_session_id);
      invalidatePendingAggregate();
      invalidateSessionLists();
    }),
  );

  unsubs.push(
    ws.on("task:completed", (p) => {
      const payload = p as TaskCompletedPayload;
      if (!payload.chat_session_id) return;
      chatWsLogger.info("task:completed (global, chat)", {
        task_id: payload.task_id,
        chat_session_id: payload.chat_session_id,
      });
      qc.setQueryData<ChatPendingTask>(
        chatKeys.pendingTask(payload.chat_session_id),
        (old) => removePendingChatTask(old, payload.task_id),
      );
      qc.invalidateQueries({ queryKey: chatKeys.pendingTask(payload.chat_session_id) });
      invalidatePendingAggregate();
    }),
  );

  unsubs.push(
    ws.on("task:failed", (p) => {
      const payload = p as TaskFailedPayload;
      if (!payload.chat_session_id) return;
      chatWsLogger.warn("task:failed (global, chat)", {
        task_id: payload.task_id,
        chat_session_id: payload.chat_session_id,
      });
      qc.setQueryData<ChatPendingTask>(
        chatKeys.pendingTask(payload.chat_session_id),
        (old) => removePendingChatTask(old, payload.task_id),
      );
      invalidateChatMessageQueries(qc, payload.chat_session_id);
      qc.invalidateQueries({ queryKey: chatKeys.pendingTask(payload.chat_session_id) });
      invalidatePendingAggregate();
      invalidateSessionLists();
    }),
  );

  unsubs.push(
    ws.on("chat:session_read", (p) => {
      const payload = p as { chat_session_id: string };
      chatWsLogger.info("chat:session_read (global)", payload);
      invalidateSessionLists();
    }),
  );

  unsubs.push(
    ws.on("chat:session_updated", (p) => {
      const payload = p as ChatSessionUpdatedPayload;
      chatWsLogger.info("chat:session_updated (global)", payload);
      const id = getCurrentWsId();
      if (!id) return;
      applyChatSessionUpdatedToCache(qc, id, payload);
    }),
  );

  unsubs.push(
    ws.on("chat:session_deleted", (p) => {
      const payload = p as { chat_session_id: string };
      chatWsLogger.info("chat:session_deleted (global)", payload);
      const id = getCurrentWsId();
      if (id) {
        const drop = (old?: { id: string }[]) =>
          old?.filter((s) => s.id !== payload.chat_session_id);
        qc.setQueryData(chatKeys.sessions(id), drop);
      }
      qc.removeQueries({ queryKey: chatKeys.messages(payload.chat_session_id) });
      qc.removeQueries({ queryKey: chatKeys.pendingTask(payload.chat_session_id) });
      invalidatePendingAggregate();

      const chatState = useChatStore.getState?.();
      if (chatState && chatState.activeSessionId === payload.chat_session_id) {
        chatState.setActiveSession(null);
      }
    }),
  );

  return () => {
    for (const unsub of unsubs) unsub();
    if (taskMessageFlushTimer) clearTimeout(taskMessageFlushTimer);
    if (aggregateRefreshTimer) clearTimeout(aggregateRefreshTimer);
  };
}
