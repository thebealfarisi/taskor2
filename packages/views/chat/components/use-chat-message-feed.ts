import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import {
  chatMessagesPageOptions,
  pendingChatTaskOptions,
} from "@multica/core/chat/queries";
import { hideQueuedChatMessages } from "@multica/core/chat/pending";
import { CHAT_VIRTUOSO_INITIAL_FIRST_ITEM_INDEX } from "./chat-controller-helpers";

export function useChatMessageFeed(activeSessionId: string | null) {
  const {
    data: rawMessagePages,
    isLoading: messagesLoading,
    fetchNextPage: fetchOlderMessages,
    hasNextPage: hasOlderMessages,
    isFetchingNextPage: isFetchingOlderMessages,
  } = useInfiniteQuery(chatMessagesPageOptions(activeSessionId ?? ""));

  const messagePages = activeSessionId ? rawMessagePages?.pages ?? [] : [];
  const allMessages = [...messagePages].reverse().flatMap((page) => page.messages);

  const { data: pendingTask, isLoading: pendingTaskLoading } = useQuery(
    pendingChatTaskOptions(activeSessionId ?? ""),
  );
  const showSkeleton =
    !!activeSessionId && (messagesLoading || pendingTaskLoading);
  const messages = hideQueuedChatMessages(allMessages, pendingTask);
  const olderMessageCount = messagePages
    .slice(1)
    .reduce((sum, page) => sum + page.messages.length, 0);
  const firstItemIndex =
    messages.length > 0
      ? CHAT_VIRTUOSO_INITIAL_FIRST_ITEM_INDEX - olderMessageCount
      : 0;
  const pendingTaskId = pendingTask?.task_id ?? null;
  const hasMessages = messages.length > 0 || !!pendingTaskId;

  return {
    messages,
    pendingTask,
    pendingTaskId,
    showSkeleton,
    hasMessages,
    firstItemIndex,
    hasOlderMessages: !!hasOlderMessages,
    isFetchingOlderMessages,
    fetchOlderMessages,
  };
}
