import type { useQueryClient } from "@tanstack/react-query";
import { chatKeys } from "@multica/core/chat/queries";
import { enqueuePendingChatTask } from "@multica/core/chat/pending";
import type { ChatPendingTask } from "@multica/core/types";

// Derive a concise session title from the first user message: first line,
// markdown stripped, whitespace collapsed, capped. A deterministic title
// (no LLM) — the server has no summarization model, so this is the sensible
// default until a runtime-generated title is wired up.
export const CHAT_TITLE_MAX = 30;

export function deriveChatTitle(content: string): string {
  const firstLine = (content.split("\n").find((l) => l.trim()) ?? content).trim();
  const cleaned = firstLine
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/[#*`>~_]/g, "")
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1") // markdown links/images → their text
    .replace(/\s+/g, " ")
    .trim();
  if (cleaned.length <= CHAT_TITLE_MAX) return cleaned;
  return cleaned.slice(0, CHAT_TITLE_MAX - 1).trimEnd() + "…";
}

/**
 * After a send resolves: is the user still composing to the target they sent
 * from? Decides whether to scrub the composer and open the sent session, or
 * treat the send as fire-and-forget (the reply surfaces as unread instead).
 */
export function isStillOnComposeTarget(
  liveActiveSessionId: string | null,
  sentFromSessionId: string | null,
): boolean {
  return liveActiveSessionId === sentFromSessionId;
}

/**
 * Decide what a project-context change should do, given the open session.
 */
export type ProjectContextChange =
  | { kind: "awaitSession" }
  | { kind: "detachCurrent"; sessionId: string }
  | { kind: "startFreshChat"; agentId: string; projectId: string }
  | { kind: "setDraftProject"; projectId: string | null };

export function planProjectContextChange(input: {
  targetProjectId: string | null;
  activeSessionId: string | null;
  currentSession: { id: string; agent_id: string } | null;
}): ProjectContextChange {
  if (input.activeSessionId) {
    if (!input.currentSession) return { kind: "awaitSession" };
    if (input.targetProjectId === null) {
      return { kind: "detachCurrent", sessionId: input.currentSession.id };
    }
    return {
      kind: "startFreshChat",
      agentId: input.currentSession.agent_id,
      projectId: input.targetProjectId,
    };
  }
  return { kind: "setDraftProject", projectId: input.targetProjectId };
}

export function hasInFlightPendingTask(
  qc: ReturnType<typeof useQueryClient>,
  sessionId: string,
): boolean {
  const pending = qc.getQueryData<ChatPendingTask>(chatKeys.pendingTask(sessionId));
  return Boolean(pending?.task_id);
}

export function seedAcceptedPendingTask(
  qc: ReturnType<typeof useQueryClient>,
  sessionId: string,
  task: {
    task_id: string;
    created_at: string;
    message_id: string;
    content: string;
    supports_queue?: boolean;
    queued?: boolean;
  },
) {
  qc.setQueryData<ChatPendingTask>(
    chatKeys.pendingTask(sessionId),
    (old) => {
      const next = enqueuePendingChatTask(old, {
        task_id: task.task_id,
        status: "queued",
        created_at: task.created_at,
        message_id: task.message_id,
        content: task.content,
      }, task.queued);
      if (task.supports_queue === true || old?.supports_queue === true) {
        next.supports_queue = true;
      }
      return next;
    },
  );
  qc.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) });
}

export const CHAT_VIRTUOSO_INITIAL_FIRST_ITEM_INDEX = 1_000_000;
