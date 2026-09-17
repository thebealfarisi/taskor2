import type { QueryClient } from "@tanstack/react-query";
import type { WSClient } from "../../api/ws-client";
import { getCurrentWsId } from "../../platform/workspace-storage";
import { issueKeys } from "../../issues/queries";
import { propertyKeys } from "../../properties/queries";
import {
  onIssueCreated,
  onIssueUpdated,
  onIssueDeleted,
  onIssueLabelsChanged,
  onIssuePropertiesChanged,
  onIssueMetadataChanged,
  onIssueAuxiliaryRevision,
} from "../../issues/ws-updaters";
import { onInboxIssueStatusChanged, onInboxIssueDeleted } from "../../inbox/ws-updaters";
import type {
  IssueCreatedPayload,
  IssueUpdatedPayload,
  IssueDeletedPayload,
  IssueLabelsChangedPayload,
  IssueAttachmentsChangedPayload,
  IssueMetadataChangedPayload,
  IssuePropertiesChangedPayload,
} from "../../types";

export function registerIssueListeners(ws: WSClient, qc: QueryClient): () => void {
  const unsubs: (() => void)[] = [];

  unsubs.push(
    ws.on("issue:updated", (p) => {
      const payload = p as IssueUpdatedPayload;
      const { issue } = payload;
      if (!issue?.id) return;
      const wsId = getCurrentWsId();
      if (wsId) {
        onIssueUpdated(qc, wsId, issue, {
          assigneeChanged: payload.assignee_changed,
          statusChanged: payload.status_changed,
          projectChanged: payload.project_changed,
        });
        if (issue.status) {
          onInboxIssueStatusChanged(qc, wsId, issue.id, issue.status);
        }
      }
    }),
  );

  unsubs.push(
    ws.on("issue:created", (p) => {
      const { issue } = p as IssueCreatedPayload;
      if (!issue) return;
      const wsId = getCurrentWsId();
      if (wsId) onIssueCreated(qc, wsId, issue);
    }),
  );

  unsubs.push(
    ws.on("issue:deleted", (p) => {
      const { issue_id } = p as IssueDeletedPayload;
      if (!issue_id) return;
      const wsId = getCurrentWsId();
      if (wsId) {
        onIssueDeleted(qc, wsId, issue_id);
        onInboxIssueDeleted(qc, wsId, issue_id);
      }
    }),
  );

  unsubs.push(
    ws.on("issue_labels:changed", (p) => {
      const { issue_id, labels, issue_revision } = p as IssueLabelsChangedPayload;
      if (!issue_id) return;
      const wsId = getCurrentWsId();
      if (wsId) onIssueLabelsChanged(qc, wsId, issue_id, labels ?? [], issue_revision);
    }),
  );

  unsubs.push(
    ws.on("issue_attachments:changed", (p) => {
      const { issue_id, issue_revision } = p as IssueAttachmentsChangedPayload;
      if (!issue_id) return;
      qc.invalidateQueries({ queryKey: issueKeys.attachments(issue_id) });
      const wsId = getCurrentWsId();
      if (wsId) onIssueAuxiliaryRevision(qc, wsId, issue_id, issue_revision);
    }),
  );

  unsubs.push(
    ws.on("issue_metadata:changed", (p) => {
      const { issue_id, metadata, issue_revision } = p as IssueMetadataChangedPayload;
      if (!issue_id) return;
      const wsId = getCurrentWsId();
      if (wsId) onIssueMetadataChanged(qc, wsId, issue_id, metadata ?? {}, issue_revision);
    }),
  );

  unsubs.push(
    ws.on("issue_properties:changed", (p) => {
      const { issue_id, properties, issue_revision } = p as IssuePropertiesChangedPayload;
      if (!issue_id) return;
      const wsId = getCurrentWsId();
      if (wsId) {
        onIssuePropertiesChanged(qc, wsId, issue_id, properties ?? {}, issue_revision);
        qc.invalidateQueries({ queryKey: propertyKeys.all(wsId) });
      }
    }),
  );

  const unsubPropertyCreated = ws.on("property:created", () => {
    const wsId = getCurrentWsId();
    if (wsId) {
      qc.invalidateQueries({ queryKey: propertyKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: issueKeys.tableAll(wsId) });
    }
  });
  const unsubPropertyUpdated = ws.on("property:updated", () => {
    const wsId = getCurrentWsId();
    if (wsId) {
      qc.invalidateQueries({ queryKey: propertyKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: issueKeys.tableAll(wsId) });
    }
  });
  unsubs.push(unsubPropertyCreated, unsubPropertyUpdated);

  return () => {
    for (const unsub of unsubs) unsub();
  };
}
