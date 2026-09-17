import { useMemo } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import type {
  Issue,
  IssueTableQuerySpec,
  WorkspaceWorkingAgent,
  WorkingAgentSummary,
} from "@multica/core/types";
import { issueTableFacetsOptions } from "@multica/core/issues/queries";
import type { IssueScope } from "@multica/core/issues/surface/scope";

interface UseIssueSurfaceWorkingAgentsInput {
  wsId: string;
  scope: IssueScope;
  usesGantt: boolean;
  tableQuerySpec: IssueTableQuerySpec;
  agentRunningFilter?: boolean;
  workspaceWorkingAgents: WorkspaceWorkingAgent[];
  ganttWorkingScopeIssues?: Issue[];
}

export function useIssueSurfaceWorkingAgents({
  wsId,
  scope,
  usesGantt,
  tableQuerySpec,
  agentRunningFilter,
  workspaceWorkingAgents,
  ganttWorkingScopeIssues,
}: UseIssueSurfaceWorkingAgentsInput): WorkingAgentSummary[] | undefined {
  // The header chip's count, kept on its own query rather than folded into the
  // submenu facet request above. Two reasons: that request is deliberately
  // lazy (an always-on facet would re-enable it for Table/grouped surfaces on
  // mount), and this spec drops `working_issue_ids` so toggling the filter
  // does not change the query identity — the number must not flicker when you
  // click the very chip it labels.
  const workingAgentsQuerySpec = useMemo<IssueTableQuerySpec>(() => {
    if (!agentRunningFilter) return tableQuerySpec;
    const { working_issue_ids: _working, ...filters } = tableQuerySpec.filters;
    return { ...tableQuerySpec, filters };
  }, [agentRunningFilter, tableQuerySpec]);

  const workingAgentsFacetRequest = useMemo(
    () => ({
      query: workingAgentsQuerySpec,
      facets: [{ kind: "working_agents" } as const],
      include_total: false,
    }),
    [workingAgentsQuerySpec],
  );

  const workingAgentsFacetQuery = useQuery({
    ...issueTableFacetsOptions(wsId, workingAgentsFacetRequest),
    placeholderData: keepPreviousData,
    // Gantt owns its own count: its canvas projection is client-side and not
    // expressible as a Table query spec, so a facet answer would over-count.
    //
    // The actor panel (member / agent detail) renders no agents-working chip at
    // all, so nothing would read the answer — and with no control to toggle it,
    // `agentRunningFilter` is unreachable there. Skip the aggregation rather
    // than pay for it on every panel mount. Adding the chip to that header
    // means dropping this clause.
    enabled: !usesGantt && scope.type !== "actor",
  });

  const facetWorkingAgents = useMemo<WorkingAgentSummary[] | undefined>(() => {
    const facet = workingAgentsFacetQuery.data?.facets.find(
      (candidate) => candidate.kind === "working_agents",
    );
    // A backend without this facet (older deploy) answers with an error or a
    // response that omits it. Stay indeterminate instead of claiming zero.
    if (!facet) return undefined;
    return facet.values.map((value) => ({
      id: value.key,
      running_task_count: value.count,
    }));
  }, [workingAgentsFacetQuery.data]);

  // Gantt draws a client-materialized canvas, so its chip counts the agents
  // holding those canvas rows. Every other view mode takes the server facet.
  return useMemo<WorkingAgentSummary[] | undefined>(() => {
    if (!usesGantt) return facetWorkingAgents;
    const rows = ganttWorkingScopeIssues;
    if (!rows) return undefined;
    const visible = new Set(rows.map((issue) => issue.id));
    const summaries: WorkingAgentSummary[] = [];
    for (const agent of workspaceWorkingAgents) {
      const running = agent.issue_ids.filter((id) => visible.has(id)).length;
      if (running > 0) {
        summaries.push({ id: agent.id, running_task_count: running });
      }
    }
    return summaries;
  }, [
    facetWorkingAgents,
    ganttWorkingScopeIssues,
    usesGantt,
    workspaceWorkingAgents,
  ]);
}
