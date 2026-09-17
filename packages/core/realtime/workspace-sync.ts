import type { QueryClient } from "@tanstack/react-query";
import { getCurrentWsId } from "../platform/workspace-storage";
import { resolvePostAuthDestination } from "../paths";
import { issueKeys } from "../issues/queries";
import { inboxKeys } from "../inbox/queries";
import { workspaceKeys, workspaceListOptions } from "../workspace/queries";
import { projectKeys } from "../projects/queries";
import { runtimeKeys } from "../runtimes/queries";
import { autopilotKeys } from "../autopilots/queries";
import {
  agentTaskSnapshotKeys,
  workspaceWorkingAgentsKeys,
  agentActivityKeys,
  agentRunCountsKeys,
} from "../agents/queries";
import { chatKeys } from "../chat/queries";
import { labelKeys } from "../labels/queries";
import { propertyKeys } from "../properties/queries";
import { issueStatusKeys } from "../issue-statuses/queries";
import { onInboxSummaryInvalidate } from "../inbox/ws-updaters";
import type { Workspace, WorkspaceUpdatedPayload } from "../types";

export function applyWorkspaceUpdatedToCache(
  qc: QueryClient,
  payload: WorkspaceUpdatedPayload,
): void {
  const next = payload.workspace;
  if (next?.id) {
    const list = qc.getQueryData<Workspace[]>(workspaceKeys.list());
    const cached = list?.find((w) => w.id === next.id) ?? null;
    if (cached && cached.issue_prefix !== next.issue_prefix) {
      qc.invalidateQueries({ queryKey: issueKeys.all(next.id) });
    }
    if (cached && list) {
      qc.setQueryData<Workspace[]>(
        workspaceKeys.list(),
        list.map((workspace) => (workspace.id === next.id ? next : workspace)),
      );
      return;
    }
    qc.invalidateQueries({ queryKey: issueKeys.all(next.id) });
  }
  qc.invalidateQueries({ queryKey: workspaceKeys.list() });
}

export function invalidateWorkspaceScopedQueries(qc: QueryClient): void {
  const wsId = getCurrentWsId();
  if (wsId) {
    qc.invalidateQueries({ queryKey: issueKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: inboxKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    qc.invalidateQueries({ queryKey: workspaceKeys.members(wsId) });
    qc.invalidateQueries({ queryKey: workspaceKeys.squads(wsId) });
    qc.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) });
    qc.invalidateQueries({ queryKey: workspaceKeys.invitations(wsId) });
    qc.invalidateQueries({ queryKey: projectKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: autopilotKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: agentTaskSnapshotKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: workspaceWorkingAgentsKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: agentActivityKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: agentRunCountsKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: chatKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: labelKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: propertyKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: issueStatusKeys.all(wsId) });
  }
  onInboxSummaryInvalidate(qc);
  qc.invalidateQueries({ queryKey: issueKeys.timelineAll() });
  qc.invalidateQueries({ queryKey: issueKeys.reactionsAll() });
  qc.invalidateQueries({ queryKey: issueKeys.subscribersAll() });
  qc.invalidateQueries({ queryKey: issueKeys.usageAll() });
  qc.invalidateQueries({ queryKey: issueKeys.attachmentsAll() });
  qc.invalidateQueries({ queryKey: issueKeys.tasksAll() });
  qc.invalidateQueries({ queryKey: chatKeys.messagesAll() });
  qc.invalidateQueries({ queryKey: chatKeys.messagesPageAll() });
  qc.invalidateQueries({ queryKey: chatKeys.pendingTaskAll() });
  qc.invalidateQueries({ queryKey: chatKeys.taskMessagesAll() });
  qc.invalidateQueries({ queryKey: chatKeys.draftRestoresAll() });
  qc.invalidateQueries({ queryKey: workspaceKeys.list() });
}

export function invalidateSquadMemberStatusQueries(qc: QueryClient, wsId: string): void {
  qc.invalidateQueries({
    predicate: (query) => {
      const key = query.queryKey;
      return (
        key[0] === "workspaces" &&
        key[1] === wsId &&
        key[2] === "squads" &&
        key[4] === "members-status"
      );
    },
  });
}

export async function relocateAfterWorkspaceLoss(
  qc: QueryClient,
  lostWsId: string,
  hasOnboarded: boolean,
): Promise<void> {
  const wsList = await qc.fetchQuery({
    ...workspaceListOptions(),
    staleTime: 0,
  });
  const remaining = wsList.filter((w) => w.id !== lostWsId);
  const target = resolvePostAuthDestination(remaining, hasOnboarded);
  if (typeof window !== "undefined") {
    window.location.assign(target);
  }
}
