import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import {
  chatKeys,
  QUICK_ACTIONS_PENDING_TIMEOUT_MS,
  sortChatSessions,
} from "../chat/queries";
import { upsertChatMessageToCaches } from "../chat/message-cache";
import { removePendingChatTask } from "../chat/pending";
import type {
  ChatCancelFinalizedPayload,
  ChatDonePayload,
  ChatMessage,
  ChatMessageEventPayload,
  ChatMessagesPage,
  ChatPendingTask,
  ChatQuickActionsFailureState,
  ChatQuickActionsPayload,
  ChatQuickActionsPendingState,
  ChatSession,
} from "../types";

export const TASK_MESSAGE_FLUSH_MS = 100;

export function invalidateChatMessageQueries(
  qc: QueryClient,
  sessionId: string,
) {
  qc.invalidateQueries({ queryKey: chatKeys.messages(sessionId) });
  qc.invalidateQueries({ queryKey: chatKeys.messagesPage(sessionId) });
}

export function refetchPendingChatAggregate(
  qc: QueryClient,
  wsId: string | null | undefined,
) {
  if (!wsId) return;
  qc.invalidateQueries({ queryKey: chatKeys.pendingTasks(wsId) });
}

export function applyChatMessageToCache(
  qc: QueryClient,
  payload: ChatMessageEventPayload,
) {
  const sessionId = payload.chat_session_id;
  if (payload.role === "user" && payload.message_id) {
    upsertChatMessageToCaches(qc, sessionId, {
      id: payload.message_id,
      chat_session_id: sessionId,
      role: "user",
      content: payload.content ?? "",
      task_id: payload.task_id ?? null,
      created_at: payload.created_at ?? new Date().toISOString(),
    });
  }
  invalidateChatMessageQueries(qc, sessionId);
  qc.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) });
}

export function applyChatDoneToCache(
  qc: QueryClient,
  payload: ChatDonePayload,
) {
  const sessionId = payload.chat_session_id;
  const taskId = payload.task_id;
  const messageId = payload.message_id;
  const content = payload.content;
  if (messageId && (content !== undefined || (payload.quick_actions?.length ?? 0) > 0)) {
    const assistant: ChatMessage = {
      id: messageId,
      chat_session_id: sessionId,
      role: "assistant",
      content: content ?? "",
      task_id: taskId,
      created_at: payload.created_at ?? new Date().toISOString(),
      elapsed_ms: payload.elapsed_ms ?? null,
      message_kind: payload.message_kind ?? "message",
      ...(payload.quick_actions !== undefined
        ? { quick_actions: payload.quick_actions }
        : {}),
    };
    upsertChatMessageToCaches(qc, sessionId, assistant);
  }
  qc.setQueryData<ChatPendingTask>(
    chatKeys.pendingTask(sessionId),
    (old) => removePendingChatTask(old, taskId),
  );
  qc.setQueryData<ChatQuickActionsPendingState | null>(
    chatKeys.quickActionsPending(sessionId),
    payload.quick_actions_pending === true && messageId
      ? {
          message_id: messageId,
          task_id: taskId,
          expires_at: Date.now() + QUICK_ACTIONS_PENDING_TIMEOUT_MS,
        }
      : null,
  );
  invalidateChatMessageQueries(qc, sessionId);
  qc.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) });
}

export async function applyChatQuickActionsToCache(
  qc: QueryClient,
  payload: ChatQuickActionsPayload,
) {
  const sessionId = payload.chat_session_id;
  const actions = payload.quick_actions ?? [];
  const patch = (m: ChatMessage): ChatMessage =>
    m.id === payload.message_id ? { ...m, quick_actions: actions } : m;
  if (actions.length > 0) {
    await Promise.all([
      qc.cancelQueries({ queryKey: chatKeys.messages(sessionId) }),
      qc.cancelQueries({ queryKey: chatKeys.messagesPage(sessionId) }),
    ]);
    qc.setQueryData<ChatMessage[] | undefined>(
      chatKeys.messages(sessionId),
      (old) => old?.map(patch),
    );
    qc.setQueryData<InfiniteData<ChatMessagesPage> | undefined>(
      chatKeys.messagesPage(sessionId),
      (old) =>
        old
          ? {
              ...old,
              pages: old.pages.map((page) => ({
                ...page,
                messages: page.messages.map(patch),
              })),
            }
          : old,
    );
    invalidateChatMessageQueries(qc, sessionId);
  }
  qc.setQueryData<ChatQuickActionsPendingState | null>(
    chatKeys.quickActionsPending(sessionId),
    (current) =>
      current && current.message_id !== payload.message_id ? current : null,
  );
  if (payload.failed === true) {
    qc.setQueryData<ChatQuickActionsFailureState | null>(
      chatKeys.quickActionsFailure(sessionId),
      { message_id: payload.message_id, at: Date.now() },
    );
  }
}

export type ChatSessionUpdatedPayload = {
  chat_session_id: string;
  title?: string;
  project_id?: string | null;
  pinned?: boolean;
  status?: "active" | "archived";
  updated_at?: string;
};

export function applyChatSessionUpdatedToCache(
  qc: QueryClient,
  wsId: string,
  payload: ChatSessionUpdatedPayload,
): void {
  qc.setQueryData<ChatSession[]>(chatKeys.sessions(wsId), (old) => {
    if (!old) return old;
    const next = old.map((s) =>
      s.id === payload.chat_session_id
        ? {
            ...s,
            title: payload.title ?? s.title,
            ...("project_id" in payload ? { project_id: payload.project_id } : {}),
            pinned: payload.pinned ?? s.pinned,
            status: payload.status ?? s.status,
            updated_at: payload.updated_at ?? s.updated_at,
            ...(payload.status === "archived"
              ? { unread_count: 0, has_unread: false }
              : {}),
          }
        : s,
    );
    return payload.pinned === undefined && payload.status === undefined
      ? next
      : sortChatSessions(next);
  });
}

export function removeChatMessageFromPageCache(
  qc: QueryClient,
  sessionId: string,
  messageId: string,
) {
  qc.setQueryData<InfiniteData<ChatMessagesPage> | undefined>(
    chatKeys.messagesPage(sessionId),
    (old) => {
      if (!old) return old;
      return {
        ...old,
        pages: old.pages.map((page) => ({
          ...page,
          messages: page.messages.filter((m) => m.id !== messageId),
        })),
      };
    },
  );
}

export function removeChatMessageFromCaches(
  qc: QueryClient,
  sessionId: string,
  messageId: string,
) {
  qc.setQueryData<ChatMessage[]>(
    chatKeys.messages(sessionId),
    (old) => old?.filter((m) => m.id !== messageId) ?? old,
  );
  removeChatMessageFromPageCache(qc, sessionId, messageId);
}

export function applyChatCancelFinalizedToCache(
  qc: QueryClient,
  payload: ChatCancelFinalizedPayload,
  currentUserId?: string,
) {
  const sessionId = payload.chat_session_id;
  if (!sessionId) return;
  if (payload.outcome === "stopped") {
    applyChatDoneToCache(qc, {
      chat_session_id: sessionId,
      task_id: payload.task_id,
      message_id: payload.message_id,
      content: payload.content,
      elapsed_ms: payload.elapsed_ms,
      created_at: payload.created_at,
      message_kind: payload.message_kind,
    });
    return;
  }
  if (payload.outcome === "restored") {
    if (payload.message_id) {
      removeChatMessageFromCaches(qc, sessionId, payload.message_id);
    }
    qc.setQueryData<ChatPendingTask>(
      chatKeys.pendingTask(sessionId),
      (old) => removePendingChatTask(old, payload.task_id),
    );
    invalidateChatMessageQueries(qc, sessionId);
    qc.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) });
    const isInitiator =
      !!payload.initiator_user_id &&
      !!currentUserId &&
      payload.initiator_user_id === currentUserId;
    if (isInitiator) {
      qc.invalidateQueries({ queryKey: chatKeys.draftRestores(sessionId) });
    }
  }
}
