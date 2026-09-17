import type { QueryClient } from "@tanstack/react-query";
import type { WSClient } from "../../api/ws-client";
import { getCurrentWsId } from "../../platform/workspace-storage";
import { issueKeys } from "../../issues/queries";
import {
  onIssueAuxiliaryRevision,
  invalidateIssueOwnerProjections,
} from "../../issues/ws-updaters";
import {
  invalidateUpdatedAtSortedIssueLists,
  invalidateLastActivitySortedIssueLists,
} from "../../issues/cache-coordinator";
import { handleInboxNew } from "../inbox-sync";
import type {
  ActivityCreatedPayload,
  CommentCreatedPayload,
  CommentDeletedPayload,
  CommentResolvedPayload,
  CommentUnresolvedPayload,
  CommentUpdatedPayload,
  InboxNewPayload,
  IssueReactionAddedPayload,
  IssueReactionRemovedPayload,
  ReactionAddedPayload,
  ReactionRemovedPayload,
  SubscriberAddedPayload,
  SubscriberRemovedPayload,
} from "../../types";

export function registerCommentListeners(ws: WSClient, qc: QueryClient): () => void {
  const unsubs: (() => void)[] = [];

  const invalidateTimeline = (issueId: string) => {
    qc.invalidateQueries({
      queryKey: issueKeys.timeline(issueId),
      refetchType: "none",
    });
  };

  unsubs.push(
    ws.on("inbox:new", async (p) => {
      const { item } = p as InboxNewPayload;
      if (!item) return;
      await handleInboxNew(qc, item);
    }),
  );

  unsubs.push(
    ws.on("comment:created", (p) => {
      const { comment, issue_revision: issueRevision } = p as CommentCreatedPayload;
      if (!comment?.issue_id) return;
      invalidateTimeline(comment.issue_id);
      const wsId = getCurrentWsId();
      if (wsId) {
        invalidateUpdatedAtSortedIssueLists(qc, wsId);
        invalidateLastActivitySortedIssueLists(qc, wsId);
        if (issueRevision) {
          onIssueAuxiliaryRevision(qc, wsId, comment.issue_id, issueRevision);
        } else {
          invalidateIssueOwnerProjections(qc, wsId, comment.issue_id);
        }
      }
    }),
  );

  unsubs.push(
    ws.on("comment:updated", (p) => {
      const { comment, issue_revision } = p as CommentUpdatedPayload;
      if (!comment?.issue_id) return;
      invalidateTimeline(comment.issue_id);
      const wsId = getCurrentWsId();
      if (wsId) {
        invalidateLastActivitySortedIssueLists(qc, wsId);
        if (issue_revision) {
          onIssueAuxiliaryRevision(qc, wsId, comment.issue_id, issue_revision);
        } else {
          invalidateIssueOwnerProjections(qc, wsId, comment.issue_id);
        }
      }
    }),
  );

  unsubs.push(
    ws.on("comment:deleted", (p) => {
      const { issue_id, issue_revision } = p as CommentDeletedPayload;
      if (!issue_id) return;
      invalidateTimeline(issue_id);
      const wsId = getCurrentWsId();
      if (wsId) {
        invalidateLastActivitySortedIssueLists(qc, wsId);
        if (issue_revision) {
          onIssueAuxiliaryRevision(qc, wsId, issue_id, issue_revision);
        } else {
          invalidateIssueOwnerProjections(qc, wsId, issue_id);
        }
      }
    }),
  );

  unsubs.push(
    ws.on("comment:resolved", (p) => {
      const { comment } = p as CommentResolvedPayload;
      if (comment?.issue_id) invalidateTimeline(comment.issue_id);
    }),
  );

  unsubs.push(
    ws.on("comment:unresolved", (p) => {
      const { comment } = p as CommentUnresolvedPayload;
      if (comment?.issue_id) invalidateTimeline(comment.issue_id);
    }),
  );

  unsubs.push(
    ws.on("activity:created", (p) => {
      const { issue_id } = p as ActivityCreatedPayload;
      if (issue_id) invalidateTimeline(issue_id);
    }),
  );

  unsubs.push(
    ws.on("reaction:added", (p) => {
      const { issue_id } = p as ReactionAddedPayload;
      if (issue_id) invalidateTimeline(issue_id);
    }),
  );

  unsubs.push(
    ws.on("reaction:removed", (p) => {
      const { issue_id } = p as ReactionRemovedPayload;
      if (issue_id) invalidateTimeline(issue_id);
    }),
  );

  unsubs.push(
    ws.on("issue_reaction:added", (p) => {
      const { issue_id, issue_revision } = p as IssueReactionAddedPayload;
      if (issue_id) {
        qc.invalidateQueries({ queryKey: issueKeys.reactions(issue_id) });
        const wsId = getCurrentWsId();
        if (wsId) onIssueAuxiliaryRevision(qc, wsId, issue_id, issue_revision);
      }
    }),
  );

  unsubs.push(
    ws.on("issue_reaction:removed", (p) => {
      const { issue_id, issue_revision } = p as IssueReactionRemovedPayload;
      if (issue_id) {
        qc.invalidateQueries({ queryKey: issueKeys.reactions(issue_id) });
        const wsId = getCurrentWsId();
        if (wsId) onIssueAuxiliaryRevision(qc, wsId, issue_id, issue_revision);
      }
    }),
  );

  unsubs.push(
    ws.on("subscriber:added", (p) => {
      const { issue_id } = p as SubscriberAddedPayload;
      if (issue_id) qc.invalidateQueries({ queryKey: issueKeys.subscribers(issue_id) });
    }),
  );

  unsubs.push(
    ws.on("subscriber:removed", (p) => {
      const { issue_id } = p as SubscriberRemovedPayload;
      if (issue_id) qc.invalidateQueries({ queryKey: issueKeys.subscribers(issue_id) });
    }),
  );

  return () => {
    for (const unsub of unsubs) unsub();
  };
}
