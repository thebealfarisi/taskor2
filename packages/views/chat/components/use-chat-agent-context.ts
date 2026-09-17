import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { useChatStore } from "@multica/core/chat";
import {
  agentListOptions,
  memberListOptions,
} from "@multica/core/workspace/queries";
import { projectListOptions } from "@multica/core/projects/queries";
import { chatSessionsOptions } from "@multica/core/chat/queries";
import { canAssignAgent } from "@multica/views/issues/components";
import {
  isAgentRuntimeBound as hasAgentRuntime,
  useAgentPresenceDetail,
  useWorkspaceAgentAvailability,
} from "@multica/core/agents";
import { useChatProjectContextSupport } from "./use-chat-project-context-support";

export function useChatAgentContext(
  wsId: string,
  activeSessionId: string | null,
) {
  const selectedAgentId = useChatStore((s) => s.selectedAgentId);
  const selectedProjectId = useChatStore((s) => s.selectedProjectId);
  const setSelectedProjectId = useChatStore((s) => s.setSelectedProjectId);
  const user = useAuthStore((s) => s.user);

  const { data: agents = [], isSuccess: agentsLoaded } = useQuery(
    agentListOptions(wsId),
  );
  const { data: members = [], isSuccess: membersLoaded } = useQuery(
    memberListOptions(wsId),
  );
  const { data: sessions = [], isSuccess: sessionsLoaded } = useQuery(
    chatSessionsOptions(wsId),
  );
  const { data: projects = [], isSuccess: projectsLoaded } = useQuery(
    projectListOptions(wsId),
  );

  const currentSession = activeSessionId
    ? sessions.find((s) => s.id === activeSessionId)
    : null;
  const isSessionArchived = currentSession?.status === "archived";
  const candidateProjectId = currentSession
    ? currentSession.project_id ?? null
    : selectedProjectId;
  const activeProjectId =
    candidateProjectId &&
    (!projectsLoaded ||
      projects.some((project) => project.id === candidateProjectId))
      ? candidateProjectId
      : null;

  // A project may be deleted on another client while this workspace's next
  // chat preference is still persisted locally. Normalize it as soon as the
  // authoritative project list settles so a future send cannot carry a stale
  // selection.
  useEffect(() => {
    if (!projectsLoaded || !selectedProjectId) return;
    if (projects.some((project) => project.id === selectedProjectId)) return;
    setSelectedProjectId(null);
  }, [projectsLoaded, projects, selectedProjectId, setSelectedProjectId]);

  const currentMember = members.find((m) => m.user_id === user?.id);
  const memberRole = currentMember?.role;
  const availableAgents = agents.filter(
    (a) => !a.archived_at && canAssignAgent(a, user?.id, memberRole),
  );
  const agentsSettled = agentsLoaded && membersLoaded;

  const sessionAgent = currentSession
    ? agents.find((a) => a.id === currentSession.agent_id) ?? null
    : null;
  const isAgentArchived = !!sessionAgent?.archived_at;

  const activeAgent =
    sessionAgent ??
    availableAgents.find((a) => a.id === selectedAgentId) ??
    availableAgents[0] ??
    null;
  const isAgentRuntimeBound = !!activeAgent && hasAgentRuntime(activeAgent);
  const isAgentAccessRevoked =
    !!activeAgent && !canAssignAgent(activeAgent, user?.id, memberRole);

  const agentAvailability = useWorkspaceAgentAvailability();
  const noAgent = agentAvailability === "none";

  const projectContextSupport = useChatProjectContextSupport(wsId, activeAgent);

  const presenceDetail = useAgentPresenceDetail(wsId, activeAgent?.id);
  const availability =
    presenceDetail === "loading" ? undefined : presenceDetail.availability;

  return {
    user,
    agents,
    availableAgents,
    agentsSettled,
    sessions,
    sessionsLoaded,
    projects,
    selectedAgentId,
    activeProjectId,
    projectContextUnsupported: projectContextSupport === false,
    currentSession,
    isSessionArchived,
    isAgentArchived,
    isAgentAccessRevoked,
    isAgentRuntimeBound,
    activeAgent,
    noAgent,
    availability,
  };
}
