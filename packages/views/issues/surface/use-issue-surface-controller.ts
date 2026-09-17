"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import type {
  Issue,
  IssueStatusCategory,
  IssueTableFacetSpec,
  IssueTableFacetsResponse,
  IssueTableGroupsRequest,
  IssueTableQuerySpec,
  Project,
  WorkingAgentSummary,
} from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { issueTableFacetsOptions } from "@multica/core/issues/queries";
import {
  buildIssueSurfaceQueryPlan,
  type IssueSurfaceQueryPlan,
} from "@multica/core/issues/surface/query-plan";
import type { IssueScope } from "@multica/core/issues/surface/scope";
import type { IssueSortParam } from "@multica/core/issues/queries";
import { propertyIdFromViewKey } from "@multica/core/issues/stores/view-store";
import type { IssueFilters } from "../utils/filter";
import type { ChildProgress } from "../components/list-row";
import type { IssueSurfaceMode } from "./types";
import type { IssueSurfaceActions } from "./actions-context";
import {
  type IssueSurfaceSelection,
  useCreateIssueSurfaceSelection,
} from "./selection-context";
import type { IssueCreateDefaults } from "./types";
import {
  useIssueSurfaceActions,
  type MoveIssueUpdates,
} from "./use-issue-surface-actions";
import { useIssueSurfaceData } from "./use-issue-surface-data";
import {
  useIssueStatusBranches,
  type IssueStatusPagination,
} from "./use-issue-status-branches";
import {
  useIssueGroupBranches,
  type IssueGroupBranches,
} from "./use-issue-group-branches";
import { useIssueSurfaceFilterSpec } from "./use-issue-surface-filter-spec";
import { useIssueSurfaceWorkingAgents } from "./use-issue-surface-working-agents";
import { exportTableIssues } from "./export-table-issues";

interface UseIssueSurfaceControllerInput {
  scope: IssueScope;
  modes: IssueSurfaceMode[];
  createDefaults?: IssueCreateDefaults;
  search?: string;
}

export interface IssueSurfaceController {
  scopeKey: string;
  projectId?: string;
  createDefaults: IssueCreateDefaults;
  viewMode: IssueSurfaceMode;
  allowGantt: boolean;
  surfaceIssues: Issue[];
  projectIssues: Issue[];
  issues: Issue[];
  swimlaneIssues: Issue[];
  /** Agents currently working inside THIS surface, under the surface's active
   *  filters — the header chip's count, so clicking it leaves exactly these
   *  agents' rows (MUL-4884, MUL-5525). `undefined` means the projection has
   *  not resolved yet; the chip renders an indeterminate state rather than a
   *  number it cannot stand behind. */
  workingAgents: WorkingAgentSummary[] | undefined;
  filteredGanttIssues: Issue[];
  sort: IssueSortParam;
  ganttIssues: Issue[];
  visibleStatuses: IssueStatusCategory[];
  hiddenStatuses: IssueStatusCategory[];
  /** Exact server counts plus cursor controls for List/status Board. */
  statusPagination?: IssueStatusPagination;
  /** Exact group catalog plus independent row cursors for Assignee/Property
   * Board and compound Swimlane cells. */
  groupBranches?: IssueGroupBranches;
  activeFilters: Omit<IssueFilters, "statusFilters">;
  /** Any filter that `clearFilters()` would reset is on. Lets an empty surface
   *  say "your filters hid everything" instead of "there is nothing here". */
  hasActiveFilters: boolean;
  actions: IssueSurfaceActions;
  selection: IssueSurfaceSelection;
  childProgressMap: Map<string, ChildProgress>;
  projectMap: Map<string, Project>;
  resolveTableExportLookups: (needs: {
    projects: boolean;
    childProgress: boolean;
  }) => Promise<{
    projectMap: Map<string, Project>;
    childProgressMap: Map<string, ChildProgress>;
  }>;
  tableSearch: string;
  /** Canonical server-owned Table membership. */
  tableQuerySpec: IssueTableQuerySpec;
  /** Exact disjunctive counts for the active server-backed filter submenu. */
  tableFacetCounts?: IssueTableFacetsResponse;
  /** Whether scopedIssues is a complete client window for local count use. */
  facetCountsExact: boolean;
  /** Load one server facet when its filter submenu is opened. */
  setActiveTableFacet: (facet: IssueTableFacetSpec | null) => void;
  setTableSearch: (query: string) => void;
  exportTableIssues: () => Promise<Issue[]>;
  isLoading: boolean;
  /** See IssueSurfaceData.isRefreshing — placeholder-backed revalidation. */
  isRefreshing: boolean;
  isEmpty: boolean;
  /**
   * The status catalog a CUSTOM status filter depends on failed to load. The
   * filter cannot be honoured, so the surface shows a retryable error rather
   * than an unexplained empty board. (MUL-6243)
   */
  isStatusCatalogError: boolean;
  /** Re-runs the failed catalog request behind {@link isStatusCatalogError}. */
  retryStatusCatalog: () => void;
  openCreateIssue: (defaults?: IssueCreateDefaults) => void;
  moveIssue: (
    issueId: string,
    updates: MoveIssueUpdates,
    onSettled?: () => void,
  ) => void;
}

export function useIssueSurfaceController({
  scope,
  modes,
  createDefaults,
  search = "",
}: UseIssueSurfaceControllerInput): IssueSurfaceController {
  const wsId = useWorkspaceId();
  const queryPlan = useMemo<IssueSurfaceQueryPlan>(
    () => buildIssueSurfaceQueryPlan(scope),
    [scope],
  );
  const scopeKey = queryPlan.scopeKey;
  const projectId = scope.type === "project" ? scope.projectId : undefined;

  const resolvedCreateDefaults = useMemo(
    () => ({ ...queryPlan.createDefaults, ...createDefaults }),
    [createDefaults, queryPlan.createDefaults],
  );

  const spec = useIssueSurfaceFilterSpec({
    wsId,
    scope,
    modes,
    projectId,
    search,
  });

  const [activeTableFacet, setActiveTableFacet] =
    useState<IssueTableFacetSpec | null>(null);

  const requestedFacets = useMemo<IssueTableFacetSpec[]>(() => {
    const facets: IssueTableFacetSpec[] = [];
    if (spec.usesServerStatusSurface) facets.push({ kind: "status" });
    if (
      activeTableFacet &&
      !facets.some(
        (facet) =>
          facet.kind === activeTableFacet.kind &&
          (facet.kind !== "property" ||
            activeTableFacet.kind !== "property" ||
            facet.property_id === activeTableFacet.property_id),
      )
    ) {
      facets.push(activeTableFacet);
    }
    // The request shape remains total while disabled.
    return facets.length > 0 ? facets : [{ kind: "status" }];
  }, [activeTableFacet, spec.usesServerStatusSurface]);

  const tableFacetRequest = useMemo(
    () => ({
      query: spec.tableQuerySpec,
      facets: requestedFacets,
      // Status surfaces consume the facet total as their authoritative empty
      // state. Table rows/groups already own the displayed total.
      include_total: spec.usesServerStatusSurface,
    }),
    [requestedFacets, spec.tableQuerySpec, spec.usesServerStatusSurface],
  );

  const tableFacetsQuery = useQuery({
    ...issueTableFacetsOptions(wsId, tableFacetRequest),
    placeholderData: keepPreviousData,
    // Counts are only visible inside one open filter submenu. Eagerly loading
    // every custom-property facet made a Table mount issue up to 47 SQL
    // statements and repeatedly scan the issue table after invalidation.
    enabled:
      spec.usesServerStatusSurface ||
      ((spec.usesTable || spec.usesServerGroupSurface) &&
        activeTableFacet !== null),
  });

  useEffect(() => {
    if (!spec.usesServerFacets) setActiveTableFacet(null);
  }, [spec.usesServerFacets]);

  const requestActiveTableFacet = useCallback(
    (facet: IssueTableFacetSpec | null) => {
      setActiveTableFacet(spec.usesServerFacets ? facet : null);
    },
    [spec.usesServerFacets],
  );

  const serverStatusBranches = useIssueStatusBranches({
    wsId,
    query: spec.tableQuerySpec,
    statuses: spec.serverStatuses,
    facets: tableFacetsQuery.data,
    facetsPending: tableFacetsQuery.isPending,
    facetsFetching: tableFacetsQuery.isFetching,
    enabled: spec.usesServerStatusSurface && !spec.statusFilterUnresolved,
  });

  const serverGroupSpec = useMemo<IssueTableGroupsRequest["group"]>(() => {
    if (spec.effectiveViewMode === "swimlane") {
      return {
        kind: "compound",
        primary: spec.swimlaneGrouping,
        // Same rollout switch as the board/list branches: `status_category` is
        // a contract this feature introduced, so it is only sent once the
        // catalog confirms this workspace HAS a custom status — which can only
        // be true if the fleet already serves this version. Otherwise the
        // swimlane keeps the exact request it made before. (MUL-6243)
        secondary: spec.hasCustomStatuses ? "status_category" : "status",
        secondary_values: spec.serverStatuses,
      };
    }
    const propertyId = propertyIdFromViewKey(spec.effectiveGrouping);
    if (propertyId) {
      return {
        kind: "property",
        property_id: propertyId,
        include_empty: true,
      };
    }
    return { kind: "assignee" };
  }, [
    spec.effectiveGrouping,
    spec.effectiveViewMode,
    spec.hasCustomStatuses,
    spec.serverStatuses,
    spec.swimlaneGrouping,
  ]);

  const serverGroupQuery = useMemo<IssueTableQuerySpec>(() => {
    if (spec.effectiveViewMode !== "swimlane") return spec.tableQuerySpec;
    const { statuses: _statuses, ...filters } = spec.tableQuerySpec.filters;
    return { ...spec.tableQuerySpec, filters };
  }, [spec.effectiveViewMode, spec.tableQuerySpec]);

  const serverGroupBranches = useIssueGroupBranches({
    wsId,
    query: serverGroupQuery,
    group: serverGroupSpec,
    secondaryValues:
      spec.effectiveViewMode === "swimlane" ? spec.serverStatuses : undefined,
    observeEmptyBranches:
      spec.effectiveViewMode === "swimlane" ||
      (spec.effectiveViewMode === "board" &&
        spec.activeGroupingProperty !== null),
    enabled: spec.usesServerGroupSurface && !spec.statusFilterUnresolved,
  });

  const selection = useCreateIssueSurfaceSelection(
    scopeKey,
    `${scopeKey}:${spec.effectiveViewMode}:${spec.membershipKey}`,
  );

  const data = useIssueSurfaceData({
    wsId,
    queryPlan,
    projectId,
    usesGantt: spec.usesGantt,
    usesTable: spec.usesTable,
    serverStatusBranches,
    serverGroupBranches,
    ganttShowCompleted: spec.ganttShowCompleted,
    statusFilters: spec.statusFilters,
    hiddenStatusCategories: spec.hiddenStatusCategories,
    statusFilterPending: spec.statusFilterPending,
    statusFilterError: spec.statusFilterError,
    priorityFilters: spec.priorityFilters,
    assigneeFilters: spec.assigneeFilters,
    includeNoAssignee: spec.includeNoAssignee,
    agentRunningFilter: spec.agentRunningFilter,
    creatorFilters: spec.creatorFilters,
    projectFilters: spec.viewProjectFilters,
    includeNoProject: spec.viewIncludeNoProject,
    labelFilters: spec.labelFilters,
    propertyFilters: spec.effectivePropertyFilters,
    workingIssueIDs: spec.workingIssueIDs,
    showSubIssues: spec.showSubIssues,
    loadProjects:
      spec.cardProperties.project ||
      (spec.usesTable &&
        spec.tableColumns.some((column) => column.key === "project")) ||
      (spec.effectiveViewMode === "swimlane" &&
        spec.swimlaneGrouping === "project"),
  });

  const workingAgents = useIssueSurfaceWorkingAgents({
    wsId,
    scope,
    usesGantt: spec.usesGantt,
    tableQuerySpec: spec.tableQuerySpec,
    agentRunningFilter: spec.agentRunningFilter,
    workspaceWorkingAgents: spec.workspaceWorkingAgents,
    ganttWorkingScopeIssues: data.ganttWorkingScopeIssues,
  });

  const handleExportTableIssues = useCallback(
    () => exportTableIssues(spec.tableQuerySpec),
    [spec.tableQuerySpec],
  );

  const { actions, openCreateIssue, moveIssue } = useIssueSurfaceActions({
    createDefaults: resolvedCreateDefaults,
  });

  const { ganttWorkingScopeIssues: _ganttWorkingScope, ...surfaceData } = data;

  return {
    scopeKey,
    projectId,
    createDefaults: resolvedCreateDefaults,
    viewMode: spec.effectiveViewMode,
    allowGantt: spec.allowedModes.has("gantt") && !!projectId,
    ...surfaceData,
    workingAgents,
    hasActiveFilters: spec.hasActiveFilters,
    statusPagination: spec.usesServerStatusSurface
      ? data.statusPagination
      : undefined,
    groupBranches: spec.usesServerGroupSurface
      ? serverGroupBranches
      : undefined,
    isEmpty:
      data.isEmpty &&
      !data.isRefreshing &&
      !(
        spec.usesTable &&
        (spec.tableSearch.trim() || spec.debouncedActiveSearch)
      ),
    isStatusCatalogError: data.isStatusCatalogError,
    retryStatusCatalog: spec.catalog.retry,
    sort: spec.sort,
    actions,
    selection,
    tableSearch: spec.tableSearch,
    tableQuerySpec: spec.tableQuerySpec,
    tableFacetCounts:
      spec.usesServerStatusSurface ||
      ((spec.usesTable || spec.usesServerGroupSurface) &&
        activeTableFacet !== null)
        ? tableFacetsQuery.data
        : undefined,
    facetCountsExact:
      !spec.usesTable &&
      !spec.usesServerStatusSurface &&
      !spec.usesServerGroupSurface,
    setActiveTableFacet: requestActiveTableFacet,
    setTableSearch: spec.setTableSearch,
    openCreateIssue,
    moveIssue,
    exportTableIssues: handleExportTableIssues,
  };
}
