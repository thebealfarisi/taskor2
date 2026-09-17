"use client";

import { useEffect, useMemo, useState } from "react";
import { hashKey, useQuery } from "@tanstack/react-query";
import type {
  IssueStatusCategory,
  IssueTableQuerySpec,
  WorkspaceWorkingAgent,
} from "@multica/core/types";
import { workspaceWorkingAgentsOptions } from "@multica/core/agents";
import { ALL_STATUSES } from "@multica/core/issues/config";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import { statusFilterColumns } from "@multica/core/issues";
import { dateOnlyToLocalDate } from "@multica/core/issues/date";
import type { IssueSortParam } from "@multica/core/issues/queries";
import {
  assigneeTypesForActorKind,
  type IssueScope,
} from "@multica/core/issues/surface/scope";
import type {
  IssueDateFilter,
  SortField,
} from "@multica/core/issues/stores/view-store";
import { propertyListOptions } from "@multica/core/properties";
import { propertyIdFromViewKey } from "@multica/core/issues/stores/view-store";
import { useViewStore } from "@multica/core/issues/stores/view-store-context";
import type { IssueSurfaceMode } from "./types";

export function issueDateFilterToApiParams(filter: IssueDateFilter | null) {
  if (!filter) return {};

  const from = dateOnlyToLocalDate(filter.from);
  const to = dateOnlyToLocalDate(filter.to);
  if (!from || !to) return {};

  const start = from <= to ? from : to;
  const endSource = from <= to ? to : from;
  const end = new Date(endSource);
  end.setDate(end.getDate() + 1);

  return {
    date_field: filter.field,
    date_start: start.toISOString(),
    date_end: end.toISOString(),
  };
}

export function useDebouncedTableSearch(value: string, delayMs = 250) {
  const [debouncedValue, setDebouncedValue] = useState(value.trim());

  useEffect(() => {
    const timer = window.setTimeout(
      () => setDebouncedValue(value.trim()),
      delayMs,
    );
    return () => window.clearTimeout(timer);
  }, [delayMs, value]);

  return debouncedValue;
}

export const EMPTY_LIST: never[] = [];

export function useStableByContent<T>(value: T): T {
  const contentKey = hashKey([value]);
  // eslint-disable-next-line react-hooks/exhaustive-deps -- identity follows the content hash, not the reference
  return useMemo(() => value, [contentKey]);
}

interface UseIssueSurfaceFilterSpecInput {
  wsId: string;
  scope: IssueScope;
  modes: IssueSurfaceMode[];
  projectId?: string;
  search?: string;
}

export function useIssueSurfaceFilterSpec({
  wsId,
  scope,
  modes,
  projectId,
  search = "",
}: UseIssueSurfaceFilterSpecInput) {
  const viewMode = useViewStore((s) => s.viewMode);
  const setViewMode = useViewStore((s) => s.setViewMode);
  const grouping = useViewStore((s) => s.grouping);
  const sortBy = useViewStore((s) => s.sortBy);
  const sortDirection = useViewStore((s) => s.sortDirection);
  const dateFilter = useViewStore((s) => s.dateFilter);
  const statusFilters = useViewStore((s) => s.statusFilters);
  const priorityFilters = useViewStore((s) => s.priorityFilters);
  const assigneeFilters = useViewStore((s) => s.assigneeFilters);
  const includeNoAssignee = useViewStore((s) => s.includeNoAssignee);
  const creatorFilters = useViewStore((s) => s.creatorFilters);
  const projectFilters = useViewStore((s) => s.projectFilters);
  const includeNoProject = useViewStore((s) => s.includeNoProject);
  const labelFilters = useViewStore((s) => s.labelFilters);
  const propertyFilters = useViewStore((s) => s.propertyFilters);
  const agentRunningFilter = useViewStore((s) => s.agentRunningFilter);
  const showSubIssues = useViewStore((s) => s.showSubIssues);
  const ganttShowCompleted = useViewStore((s) => s.ganttShowCompleted);
  const cardProperties = useViewStore((s) => s.cardProperties);
  const swimlaneGrouping = useViewStore((s) => s.swimlaneGrouping);
  const tableColumns = useViewStore((s) => s.tableColumns);
  const listCollapsedStatuses = useViewStore((s) => s.listCollapsedStatuses);
  const hiddenStatusCategories = useViewStore((s) => s.hiddenStatusCategories);
  const catalog = useIssueStatuses(wsId);
  const { hasCustomStatuses } = catalog;
  const [tableSearch, setTableSearch] = useState("");

  const allowedModes = useMemo(() => new Set<IssueSurfaceMode>(modes), [modes]);
  const fallbackMode = modes[0] ?? "list";
  const effectiveViewMode = allowedModes.has(viewMode as IssueSurfaceMode)
    ? (viewMode as IssueSurfaceMode)
    : fallbackMode;

  useEffect(() => {
    if (!allowedModes.has(viewMode as IssueSurfaceMode)) {
      setViewMode(fallbackMode);
    }
  }, [allowedModes, fallbackMode, setViewMode, viewMode]);

  const dateParams = useMemo(
    () => issueDateFilterToApiParams(dateFilter),
    [dateFilter],
  );

  const { data: workspaceProperties = EMPTY_LIST, isSuccess: catalogSettled } =
    useQuery(propertyListOptions(wsId));
  const activePropertyIds = useMemo(
    () => new Set(workspaceProperties.map((p) => p.id)),
    [workspaceProperties],
  );
  const effectivePropertyFilters = useMemo(() => {
    if (!catalogSettled) return propertyFilters;
    const entries = Object.entries(propertyFilters).filter(
      ([propertyId, selected]) =>
        selected.length > 0 && activePropertyIds.has(propertyId),
    );
    if (entries.length === Object.keys(propertyFilters).length) {
      return propertyFilters;
    }
    return Object.fromEntries(entries);
  }, [activePropertyIds, catalogSettled, propertyFilters]);

  const rawPropertySortId = propertyIdFromViewKey(sortBy);
  const propertySortId =
    rawPropertySortId &&
    (!catalogSettled || activePropertyIds.has(rawPropertySortId))
      ? rawPropertySortId
      : null;
  const sort = useMemo<IssueSortParam>(() => {
    const sortBy_: IssueSortParam["sort_by"] = propertySortId
      ? `property:${propertySortId}`
      : rawPropertySortId
        ? "position"
        : (sortBy as Exclude<SortField, `property:${string}`>);
    return {
      sort_by: sortBy_,
      sort_direction: sortBy_ !== "position" ? sortDirection : undefined,
      ...dateParams,
      ...(Object.keys(effectivePropertyFilters).length > 0
        ? { properties: effectivePropertyFilters }
        : {}),
    };
  }, [
    dateParams,
    effectivePropertyFilters,
    propertySortId,
    rawPropertySortId,
    sortBy,
    sortDirection,
  ]);

  const groupingPropertyId = propertyIdFromViewKey(grouping);
  const activeGroupingProperty = groupingPropertyId
    ? workspaceProperties.find(
        (property) =>
          property.id === groupingPropertyId && property.type === "select",
      ) ?? null
    : null;
  const effectiveGrouping =
    groupingPropertyId && catalogSettled && !activeGroupingProperty
      ? "status"
      : grouping;
  const usesGantt = effectiveViewMode === "gantt" && !!projectId;
  const usesTable = effectiveViewMode === "table";
  const activeSearch = usesTable ? tableSearch : search;
  const debouncedActiveSearch = useDebouncedTableSearch(activeSearch);
  const usesServerStatusSurface =
    effectiveViewMode === "list" ||
    (effectiveViewMode === "board" && effectiveGrouping === "status");
  const usesServerGroupSurface =
    (effectiveViewMode === "board" && effectiveGrouping !== "status") ||
    effectiveViewMode === "swimlane";
  const usesServerFacets =
    usesTable || usesServerStatusSurface || usesServerGroupSurface;
  const statusColumnsForFilters = useMemo(
    () => statusFilterColumns(statusFilters, catalog),
    [catalog, statusFilters],
  );

  const statusFilterPending = statusColumnsForFilters.state === "pending";
  const statusFilterError = statusColumnsForFilters.state === "error";
  const statusFilterUnresolved = statusFilterPending || statusFilterError;

  const serverStatuses = useMemo<IssueStatusCategory[]>(() => {
    const selected =
      statusFilters.length > 0 && statusColumnsForFilters.state === "resolved"
        ? statusColumnsForFilters.columns
        : null;
    const visible = ALL_STATUSES.filter(
      (category) =>
        !hiddenStatusCategories.includes(category) &&
        (selected === null || selected.has(category)),
    );
    return effectiveViewMode === "list"
      ? visible.filter((status) => !listCollapsedStatuses.includes(status))
      : visible;
  }, [
    effectiveViewMode,
    hiddenStatusCategories,
    listCollapsedStatuses,
    statusColumnsForFilters,
    statusFilters,
  ]);

  const projectFilterState = useMemo(
    () => ({
      projectFilters: scope.type === "project" ? [] : projectFilters,
      includeNoProject: scope.type === "project" ? false : includeNoProject,
    }),
    [includeNoProject, projectFilters, scope.type],
  );
  const {
    projectFilters: viewProjectFilters,
    includeNoProject: viewIncludeNoProject,
  } = projectFilterState;

  const hasActiveFilters =
    statusFilters.length > 0 ||
    priorityFilters.length > 0 ||
    assigneeFilters.length > 0 ||
    includeNoAssignee ||
    creatorFilters.length > 0 ||
    viewProjectFilters.length > 0 ||
    viewIncludeNoProject ||
    labelFilters.length > 0 ||
    Object.keys(effectivePropertyFilters).length > 0 ||
    dateFilter != null ||
    agentRunningFilter === true;

  const workingAgentMineRelation =
    scope.type === "my"
      ? scope.relation === "all"
        ? "any"
        : scope.relation
      : undefined;
  const { data: workspaceWorkingAgents = EMPTY_LIST } = useQuery(
    workspaceWorkingAgentsOptions(wsId, "issue", workingAgentMineRelation),
  );
  const workingIssueIDs = useMemo(() => {
    const issueIDs = new Set<string>();
    for (const agent of workspaceWorkingAgents as WorkspaceWorkingAgent[]) {
      for (const issueID of agent.issue_ids) issueIDs.add(issueID);
    }
    return issueIDs;
  }, [workspaceWorkingAgents]);

  const derivedTableQuerySpec = useMemo<IssueTableQuerySpec>(() => {
    let queryScope: IssueTableQuerySpec["scope"];
    switch (scope.type) {
      case "workspace": {
        const assigneeTypes = assigneeTypesForActorKind(scope.actorKind);
        queryScope = {
          kind: "workspace",
          ...(assigneeTypes ? { assignee_types: assigneeTypes } : {}),
        };
        break;
      }
      case "project": {
        const assigneeTypes = assigneeTypesForActorKind(scope.actorKind);
        queryScope = {
          kind: "project",
          project_id: scope.projectId,
          ...(assigneeTypes ? { assignee_types: assigneeTypes } : {}),
        };
        break;
      }
      case "my":
        queryScope = {
          kind: "my",
          relation: scope.relation === "all" ? "any" : scope.relation,
        };
        break;
      case "actor":
        queryScope = {
          kind: scope.relation === "assigned" ? "assignee" : "creator",
          actor: { type: scope.actorType, id: scope.actorId },
        };
        break;
      case "team":
        throw new Error("Team issue scope is not supported by the Table query");
    }

    const date =
      dateParams.date_field && dateParams.date_start && dateParams.date_end
        ? {
            field: dateParams.date_field,
            start: dateParams.date_start,
            end: dateParams.date_end,
          }
        : undefined;
    return {
      scope: queryScope,
      filters: {
        ...(statusFilters.length > 0 ? { statuses: statusFilters } : {}),
        ...(priorityFilters.length > 0 ? { priorities: priorityFilters } : {}),
        ...(assigneeFilters.length > 0 ? { assignees: assigneeFilters } : {}),
        ...(includeNoAssignee ? { include_no_assignee: true } : {}),
        ...(creatorFilters.length > 0 ? { creators: creatorFilters } : {}),
        ...(viewProjectFilters.length > 0
          ? { project_ids: viewProjectFilters }
          : {}),
        ...(viewIncludeNoProject ? { include_no_project: true } : {}),
        ...(labelFilters.length > 0 ? { label_ids: labelFilters } : {}),
        ...(Object.keys(effectivePropertyFilters).length > 0
          ? { properties: effectivePropertyFilters }
          : {}),
        ...(date ? { date } : {}),
        ...(agentRunningFilter
          ? { working_issue_ids: [...workingIssueIDs] }
          : {}),
        include_sub_issues: showSubIssues,
      },
      ...(debouncedActiveSearch ? { search: debouncedActiveSearch } : {}),
      sort: {
        field: sort.sort_by ?? "position",
        direction: sort.sort_direction ?? "asc",
      },
    };
  }, [
    agentRunningFilter,
    assigneeFilters,
    creatorFilters,
    dateParams,
    debouncedActiveSearch,
    effectivePropertyFilters,
    includeNoAssignee,
    labelFilters,
    priorityFilters,
    scope,
    showSubIssues,
    sort.sort_by,
    sort.sort_direction,
    statusFilters,
    viewIncludeNoProject,
    viewProjectFilters,
    workingIssueIDs,
  ]);

  const tableQuerySpec = useStableByContent(derivedTableQuerySpec);

  const membershipKey = useMemo(
    () =>
      JSON.stringify([
        statusFilters,
        priorityFilters,
        assigneeFilters,
        includeNoAssignee,
        creatorFilters,
        viewProjectFilters,
        viewIncludeNoProject,
        labelFilters,
        effectivePropertyFilters,
        agentRunningFilter,
        showSubIssues,
        dateParams,
        debouncedActiveSearch,
      ]),
    [
      agentRunningFilter,
      assigneeFilters,
      creatorFilters,
      dateParams,
      debouncedActiveSearch,
      effectivePropertyFilters,
      includeNoAssignee,
      labelFilters,
      priorityFilters,
      showSubIssues,
      statusFilters,
      viewIncludeNoProject,
      viewProjectFilters,
    ],
  );

  return {
    effectiveViewMode,
    allowedModes,
    tableSearch,
    setTableSearch,
    debouncedActiveSearch,
    sort,
    activeGroupingProperty,
    effectiveGrouping,
    usesGantt,
    usesTable,
    usesServerStatusSurface,
    usesServerGroupSurface,
    usesServerFacets,
    catalog,
    hasCustomStatuses,
    statusFilterPending,
    statusFilterError,
    statusFilterUnresolved,
    serverStatuses,
    viewProjectFilters,
    viewIncludeNoProject,
    hasActiveFilters,
    workspaceWorkingAgents: workspaceWorkingAgents as WorkspaceWorkingAgent[],
    workingIssueIDs,
    tableQuerySpec,
    membershipKey,
    // view store subscriptions
    ganttShowCompleted,
    statusFilters,
    hiddenStatusCategories,
    priorityFilters,
    assigneeFilters,
    includeNoAssignee,
    agentRunningFilter,
    creatorFilters,
    labelFilters,
    effectivePropertyFilters,
    showSubIssues,
    cardProperties,
    swimlaneGrouping,
    tableColumns,
  };
}
