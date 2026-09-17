package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/slack"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// buildClaimedTaskResponse assembles the full daemon claim payload for a
// single already-claimed task and computes the exact comment ids embedded in
// it (deliveredCommentIDs). Shared by the per-runtime handler
// (ClaimTaskByRuntime) and the machine-level batch handler
// (ClaimTasksByRuntime, MUL-4257) so both build byte-identical payloads and
// feed the same delivery receipt into FinalizeTaskClaim. A non-nil failure
// means the task must not be dispatched; the builder has already cancelled it
// where the failure semantics require it.
func (h *Handler) buildClaimedTaskResponse(r *http.Request, task *db.AgentTaskQueue, runtime db.AgentRuntime, runtimeID, runtimeWorkspaceID string) (resp AgentTaskResponse, deliveredCommentIDs []pgtype.UUID, agentSkillCount, builtinSkillCount int, failure *claimBuildFailure) {
	// Build response with fresh agent data (name + skills + custom_env + custom_args).
	resp = taskToResponse(*task, runtimeWorkspaceID)
	// Claim-only capability: this server resolves the squad-leader role on the
	// wire (is_leader_task / squad_id), so the daemon must not re-derive it
	// from the briefing text. Set unconditionally — on every claim, leader or
	// not — because its absence is what tells an upgraded daemon it is talking
	// to a server too old to have answered the question (MUL-5811).
	resp.LeaderRoleResolved = true
	// Agent-trigger plugin hooks, as tools. A failure here degrades to no
	// tools rather than failing the claim: a plugin that cannot be listed must
	// not stop an agent from working on the issue.
	if h.PluginService != nil && h.pluginsV1Enabled(r.Context()) {
		if tools, toolErr := h.PluginService.AgentHookTools(r.Context(), parseUUID(runtimeWorkspaceID)); toolErr != nil {
			slog.Warn("plugins: could not list agent hook tools", "workspace_id", runtimeWorkspaceID, "error", toolErr)
		} else {
			resp.PluginHookTools = tools
		}
		// mcp-transport hooks ride the EXISTING broker: the connection shape is
		// what validatePinnedRemoteMCPTools already reads, so an approved tool
		// that went missing or whose schema drifted refuses at startup without
		// any new enforcement code.
		if connections, connErr := h.PluginService.AgentMCPConnections(r.Context(), parseUUID(runtimeWorkspaceID)); connErr != nil {
			slog.Warn("plugins: could not list agent MCP connections", "workspace_id", runtimeWorkspaceID, "error", connErr)
		} else if len(connections) > 0 {
			resp.RemoteMCPConnections = append(resp.RemoteMCPConnections, connections...)
		}
	}
	supportsCoalescedComments := requestHasClientCapability(r, protocol.DaemonCapabilityCoalescedCommentsV1)
	// Empty-but-non-nil so pgx persists '{}' rather than NULL for tasks without
	// comment input. Comment tasks replace this with the ids actually embedded
	// in the capability-aware response built below.
	deliveredCommentIDs = []pgtype.UUID{}
	composioMCPEnabled := h.composioMCPAppsEnabled(r.Context())
	if composioMCPEnabled {
		resp.ConnectedApps = parseRuntimeConnectedAppsForClaim(task.RuntimeConnectedApps, task.ID)
	}

	useSkillRefs := requestHasClientCapability(r, protocol.DaemonCapabilitySkillBundlesV1)
	agentSkillCount, builtinSkillCount, failure = h.resolveClaimAgentData(r, task, runtime, runtimeID, composioMCPEnabled, useSkillRefs, &resp)
	if failure != nil {
		return resp, deliveredCommentIDs, agentSkillCount, builtinSkillCount, failure
	}

	h.resolveClaimInitiatorAndOwner(r, task, runtime, runtimeID, &resp)

	if task.IssueID.Valid {
		deliveredCommentIDs = h.resolveClaimIssueContext(r, task, runtime, supportsCoalescedComments, &resp)
	}

	if task.ChatSessionID.Valid {
		if failure := h.resolveClaimChatContext(r, task, &resp); failure != nil {
			return resp, deliveredCommentIDs, agentSkillCount, builtinSkillCount, failure
		}
	}

	if task.AutopilotRunID.Valid {
		h.resolveClaimAutopilotContext(r.Context(), task, &resp)
	}

	hasQuickCreate := false
	if task.Context != nil && !task.IssueID.Valid && !task.ChatSessionID.Valid && !task.AutopilotRunID.Valid {
		hasQuickCreate = h.resolveClaimQuickCreateContext(r, task, &resp)
	}

	if failure := h.verifyClaimIsolationAndWorktree(r, task, runtime, runtimeID, runtimeWorkspaceID, hasQuickCreate, &resp); failure != nil {
		return resp, deliveredCommentIDs, agentSkillCount, builtinSkillCount, failure
	}

	return resp, deliveredCommentIDs, agentSkillCount, builtinSkillCount, nil
}

func (h *Handler) resolveClaimAgentData(r *http.Request, task *db.AgentTaskQueue, runtime db.AgentRuntime, runtimeID string, composioMCPEnabled, useSkillRefs bool, resp *AgentTaskResponse) (agentSkillCount, builtinSkillCount int, failure *claimBuildFailure) {
	agent, err := h.Queries.GetAgent(r.Context(), task.AgentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Error("daemon claim: task agent no longer exists; refusing dispatch",
				"task_id", uuidToString(task.ID), "agent_id", uuidToString(task.AgentID))
			return agentSkillCount, builtinSkillCount, h.failClaimedTaskBeforeLaunch(
				r.Context(), task,
				"Task identity is invalid: the assigned agent no longer exists.",
				taskfailure.ReasonInvalidTaskIdentity,
				"error_invalid_task_identity", http.StatusConflict, "task agent no longer exists",
			)
		}
		slog.Error("daemon claim: load task agent failed; requeueing claim",
			"task_id", uuidToString(task.ID), "agent_id", uuidToString(task.AgentID), "error", err)
		if _, requeueErr := h.TaskService.RequeueTaskAfterClaimFailure(r.Context(), *task); requeueErr != nil {
			slog.Error("daemon claim: requeue after agent load failure failed; stale reclaim will recover it",
				"task_id", uuidToString(task.ID), "error", requeueErr)
		}
		return agentSkillCount, builtinSkillCount, &claimBuildFailure{
			outcome: "error_load_task_agent",
			status:  http.StatusInternalServerError,
			message: "failed to load task agent",
		}
	}
	var customEnv map[string]string
	if agent.CustomEnv != nil {
		if err := json.Unmarshal(agent.CustomEnv, &customEnv); err != nil {
			slog.Warn("failed to unmarshal agent custom_env", "agent_id", uuidToString(agent.ID), "error", err)
		}
	}
	var customArgs []string
	if agent.CustomArgs != nil {
		if err := json.Unmarshal(agent.CustomArgs, &customArgs); err != nil {
			slog.Warn("failed to unmarshal agent custom_args", "agent_id", uuidToString(agent.ID), "error", err)
		}
	}
	var mcpConfig json.RawMessage
	if agent.McpConfig != nil {
		mcpConfig = json.RawMessage(agent.McpConfig)
	}
	if bound, err := h.Queries.ListEnabledAgentMcpServers(r.Context(), agent.ID); err != nil {
		slog.Warn("daemon claim: load agent mcp servers failed; using agent mcp_config",
			"task_id", uuidToString(task.ID), "agent_id", uuidToString(agent.ID), "error", err)
	} else if len(bound) > 0 {
		bindings := make([]WorkspaceMcpBinding, 0, len(bound))
		for _, server := range bound {
			bindings = append(bindings, WorkspaceMcpBinding{Name: server.Name, Config: json.RawMessage(server.Config)})
		}
		if resolved, err := ResolveAgentMcpConfig(bindings, mcpConfig); err != nil {
			slog.Warn("daemon claim: resolve agent mcp servers failed; falling back to agent mcp_config",
				"task_id", uuidToString(task.ID), "agent_id", uuidToString(agent.ID), "error", err)
		} else {
			mcpConfig = resolved
		}
	}
	if composioMCPEnabled && len(task.RuntimeMcpOverlay) > 0 {
		if merged, err := mergeMCPOverlay(mcpConfig, json.RawMessage(task.RuntimeMcpOverlay)); err != nil {
			slog.Warn("daemon claim: merge runtime_mcp_overlay failed; falling back to agent mcp_config", "task_id", uuidToString(task.ID), "error", err)
		} else {
			mcpConfig = merged
		}
	}
	var runtimeConfig json.RawMessage
	if rc := bytes.TrimSpace(agent.RuntimeConfig); len(rc) > 0 && !bytes.Equal(rc, []byte("{}")) && !bytes.Equal(rc, []byte("null")) {
		runtimeConfig = json.RawMessage(agent.RuntimeConfig)
	}
	resp.Agent = &TaskAgentData{
		ID:                    uuidToString(agent.ID),
		Name:                  agent.Name,
		Instructions:          agent.Instructions,
		CustomEnv:             customEnv,
		CustomArgs:            customArgs,
		McpConfig:             mcpConfig,
		Model:                 agent.Model.String,
		ThinkingLevel:         agent.ThinkingLevel.String,
		ServiceTier:           agent.ServiceTier.String,
		RuntimeConfig:         runtimeConfig,
		DisabledRuntimeSkills: disabledRuntimeSkillsFor(agent.DisabledRuntimeSkills, runtimeID, runtime.Provider),
	}
	if agent.SystemKey.String == service.MikaSystemKey {
		resp.Agent.Instructions = service.ComposeMikaInstructions(agent.Name, agent.Instructions)
	}
	if useSkillRefs {
		_, skillRefs := h.TaskService.LoadAgentSkillBundles(r.Context(), task.AgentID)
		agentSkillCount = len(skillRefs)
		resp.Agent.SkillRefs = skillRefs
	} else {
		skills := h.TaskService.LoadAgentSkills(r.Context(), task.AgentID)
		agentSkillCount = len(skills)
		builtinSkills := h.TaskService.BuiltinSkills()
		builtinSkillCount = len(builtinSkills)
		skills = append(skills, builtinSkills...)
		resp.Agent.Skills = skills
	}
	if !claimResponseAgentIdentityMatches(*resp) {
		responseAgentID := ""
		if resp.Agent != nil {
			responseAgentID = resp.Agent.ID
		}
		slog.Error("daemon claim: response agent identity mismatch; refusing dispatch",
			"task_id", uuidToString(task.ID), "task_agent_id", resp.AgentID, "response_agent_id", responseAgentID)
		return agentSkillCount, builtinSkillCount, h.failClaimedTaskBeforeLaunch(
			r.Context(), task,
			"Task identity is invalid: the task and response agent disagree.",
			taskfailure.ReasonInvalidTaskIdentity,
			"error_invalid_task_identity", http.StatusConflict, "task response agent identity mismatch",
		)
	}
	return agentSkillCount, builtinSkillCount, nil
}

func (h *Handler) resolveClaimInitiatorAndOwner(r *http.Request, task *db.AgentTaskQueue, runtime db.AgentRuntime, runtimeID string, resp *AgentTaskResponse) {
	if runtime.OwnerID.Valid {
		if owner, err := h.Queries.GetUser(r.Context(), runtime.OwnerID); err == nil {
			resp.RequestingUserName = owner.Name
			resp.RequestingUserProfileDescription = owner.ProfileDescription
		} else {
			slog.Debug("failed to load runtime owner for brief injection",
				"runtime_id", runtimeID,
				"owner_id", uuidToString(runtime.OwnerID),
				"error", err,
			)
		}
	}
	if task.InitiatorUserID.Valid {
		resp.InitiatorType = "member"
		resp.InitiatorID = uuidToString(task.InitiatorUserID)
		if u, err := h.Queries.GetUser(r.Context(), task.InitiatorUserID); err == nil {
			resp.InitiatorName = u.Name
			resp.InitiatorEmail = u.Email
		}
	}
}

func (h *Handler) resolveIssueSquadLeaderBriefing(ctx context.Context, task *db.AgentTaskQueue, issue db.Issue, resp *AgentTaskResponse) {
	if !task.IsLeaderTask {
		return
	}
	injected := false
	if resp.Agent != nil && task.SquadID.Valid {
		if squad, err := h.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
			ID:          task.SquadID,
			WorkspaceID: issue.WorkspaceID,
		}); err == nil && uuidToString(squad.LeaderID) == resp.Agent.ID {
			ownsIssueStatus := issue.AssigneeType.Valid &&
				issue.AssigneeType.String == "squad" &&
				uuidToString(issue.AssigneeID) == uuidToString(squad.ID)
			briefing := buildSquadLeaderBriefing(ctx, h.Queries, squad, ownsIssueStatus)
			if strings.TrimSpace(resp.Agent.Instructions) == "" {
				resp.Agent.Instructions = briefing
			} else {
				resp.Agent.Instructions = resp.Agent.Instructions + "\n\n" + briefing
			}
			injected = true
			slog.Debug("injected squad leader briefing",
				"squad_id", uuidToString(squad.ID),
				"squad_name", squad.Name,
				"leader_agent_id", resp.Agent.ID,
				"owns_issue_status", ownsIssueStatus,
			)
		}
	}
	if !injected {
		resp.IsLeaderTask = false
		slog.Warn("squad leader briefing not injected; claim delivered as a non-leader task",
			"task_id", uuidToString(task.ID),
			"squad_id", uuidToString(task.SquadID),
			"agent_id", uuidToString(task.AgentID),
		)
	}
}

func (h *Handler) resolveIssueComments(r *http.Request, task *db.AgentTaskQueue, runtime db.AgentRuntime, supportsCoalescedComments bool, resp *AgentTaskResponse) []pgtype.UUID {
	plannedCommentIDs := append([]pgtype.UUID{}, task.CoalescedCommentIds...)
	if task.TriggerCommentID.Valid {
		plannedCommentIDs = append(plannedCommentIDs, task.TriggerCommentID)
	}
	loadedComments := h.buildCoalescedCommentData(r.Context(), runtime.WorkspaceID, plannedCommentIDs)
	triggerCommentID := uuidToString(task.TriggerCommentID)
	var deliveredComments []CoalescedCommentData
	triggerLoaded := false
	for _, comment := range loadedComments {
		if comment.ID == triggerCommentID {
			triggerLoaded = true
			break
		}
	}
	if task.TriggerCommentID.Valid && triggerLoaded {
		deliveredComments = selectCommentDelivery(
			loadedComments,
			triggerCommentID,
			!supportsCoalescedComments,
			maxClaimCommentPayloadBytes,
		)
	}
	deliveredCommentIDs := commentDataIDs(deliveredComments)
	resp.CoalescedCommentIDs = nil
	for _, comment := range deliveredComments {
		if comment.ID == triggerCommentID {
			resp.TriggerCommentContent = comment.Content
			resp.TriggerThreadID = comment.ThreadID
			resp.TriggerAuthorType = comment.AuthorType
			resp.TriggerAuthorName = comment.AuthorName
			continue
		}
		resp.CoalescedCommentIDs = append(resp.CoalescedCommentIDs, comment.ID)
		resp.CoalescedComments = append(resp.CoalescedComments, comment)
	}

	effectiveTriggerUUID := task.TriggerCommentID
	if effectiveTriggerUUID.Valid {
		if comment, err := h.Queries.GetCommentInWorkspace(r.Context(), db.GetCommentInWorkspaceParams{
			ID:          effectiveTriggerUUID,
			WorkspaceID: runtime.WorkspaceID,
		}); err == nil {
			resp.TriggerCommentContent = comment.Content
			resp.TriggerThreadID = uuidToString(comment.ID)
			if comment.ParentID.Valid {
				resp.TriggerThreadID = uuidToString(comment.ParentID)
			}
			resp.TriggerAuthorType = comment.AuthorType
			resp.InitiatorType = comment.AuthorType
			if comment.AuthorID.Valid {
				resp.InitiatorID = uuidToString(comment.AuthorID)
			}
			switch comment.AuthorType {
			case "agent":
				if comment.AuthorID.Valid {
					if a, err := h.Queries.GetAgent(r.Context(), comment.AuthorID); err == nil {
						resp.TriggerAuthorName = a.Name
						resp.InitiatorName = a.Name
					}
				}
			case "member":
				if comment.AuthorID.Valid {
					if u, err := h.Queries.GetUser(r.Context(), comment.AuthorID); err == nil {
						resp.TriggerAuthorName = u.Name
						resp.InitiatorName = u.Name
						resp.InitiatorEmail = u.Email
					}
				}
			}
			if startedAt, err := h.Queries.GetLastTaskStartedAtForIssueAndAgent(r.Context(), db.GetLastTaskStartedAtForIssueAndAgentParams{
				AgentID: task.AgentID,
				IssueID: comment.IssueID,
			}); err == nil && startedAt.Valid {
				if cnt, err := h.Queries.CountNewCommentsSince(r.Context(), db.CountNewCommentsSinceParams{
					AnchorID:    effectiveTriggerUUID,
					IssueID:     comment.IssueID,
					WorkspaceID: comment.WorkspaceID,
					Since:       startedAt,
					AuthorID:    task.AgentID,
				}); err == nil && cnt > 0 {
					resp.NewCommentCount = int(cnt)
					resp.NewCommentsSince = startedAt.Time.UTC().Format(time.RFC3339)
				}
			}
		}
	}

	if !supportsCoalescedComments {
		if len(resp.CoalescedComments) > 0 || (resp.TriggerCommentContent == "" && len(deliveredComments) > 0) {
			resp.TriggerCommentContent = formatLegacyCommentBundle(deliveredComments)
		}
		resp.CoalescedCommentIDs = nil
		resp.CoalescedComments = nil
	} else if resp.TriggerCommentContent == "" && len(deliveredComments) > 0 {
		resp.TriggerCommentContent = "The newest triggering comment is no longer available. Address every earlier comment included below."
	}
	return deliveredCommentIDs
}

func (h *Handler) resolveIssuePriorSession(r *http.Request, task *db.AgentTaskQueue, resp *AgentTaskResponse) {
	if task.RerunOfTaskID.Valid {
		if src, err := h.Queries.GetAgentTask(r.Context(), task.RerunOfTaskID); err == nil && rerunSourceMatchesTaskScope(*task, src) {
			if src.WorkDir.Valid {
				resp.PriorWorkDir = src.WorkDir.String
			}
			if !service.ResumeUnsafeFailure(src.FailureReason.String, src.Error.String) &&
				src.SessionID.Valid && src.RuntimeID == task.RuntimeID {
				resp.PriorSessionID = src.SessionID.String
			}
			if src.SessionRolloutMissing {
				resp.PriorSessionResumeUnavailable = true
			}
		} else if err == nil {
			slog.Warn("daemon claim: rerun source belongs to another agent or scope; starting fresh",
				"task_id", uuidToString(task.ID),
				"task_agent_id", uuidToString(task.AgentID),
				"task_issue_id", uuidToString(task.IssueID),
				"source_task_id", uuidToString(src.ID),
				"source_agent_id", uuidToString(src.AgentID),
				"source_issue_id", uuidToString(src.IssueID),
			)
			resp.PriorSessionResumeUnavailable = true
		}
	} else if !task.ForceFreshSession {
		if prior, err := h.Queries.GetLastTaskSession(r.Context(), db.GetLastTaskSessionParams{
			AgentID: task.AgentID,
			IssueID: task.IssueID,
		}); err == nil && prior.SessionID.Valid {
			if prior.RuntimeID == task.RuntimeID {
				resp.PriorSessionID = prior.SessionID.String
			}
			if prior.WorkDir.Valid {
				resp.PriorWorkDir = prior.WorkDir.String
			}
		}
		if missing, err := h.Queries.GetLatestTaskRolloutMissing(r.Context(), db.GetLatestTaskRolloutMissingParams{
			AgentID: task.AgentID,
			IssueID: task.IssueID,
		}); err == nil && missing {
			resp.PriorSessionResumeUnavailable = true
		}
	}
}

func (h *Handler) resolveClaimIssueContext(r *http.Request, task *db.AgentTaskQueue, runtime db.AgentRuntime, supportsCoalescedComments bool, resp *AgentTaskResponse) []pgtype.UUID {
	if issue, err := h.Queries.GetIssue(r.Context(), task.IssueID); err == nil {
		resp.WorkspaceID = uuidToString(issue.WorkspaceID)
		resp.ThreadName = issue.Title
		h.resolveIssueSquadLeaderBriefing(r.Context(), task, issue, resp)

		var projectRepos []RepoData
		if issue.ProjectID.Valid {
			resp.ProjectID = uuidToString(issue.ProjectID)
			if proj, err := h.Queries.GetProject(r.Context(), issue.ProjectID); err == nil {
				resp.ProjectTitle = proj.Title
				resp.ProjectDescription = proj.Description.String
			}
			resources, repos := h.resolveProjectResourcesAndRepos(r.Context(), issue.ProjectID)
			if len(resources) > 0 {
				resp.ProjectResources = resources
				projectRepos = repos
			}
		}

		if len(projectRepos) > 0 {
			resp.Repos = projectRepos
		} else if ws, err := h.Queries.GetWorkspace(r.Context(), issue.WorkspaceID); err == nil && ws.Repos != nil {
			var repos []RepoData
			if json.Unmarshal(ws.Repos, &repos) == nil && len(repos) > 0 {
				resp.Repos = repos
			}
		}
	}

	deliveredCommentIDs := h.resolveIssueComments(r, task, runtime, supportsCoalescedComments, resp)
	h.resolveIssuePriorSession(r, task, resp)
	return deliveredCommentIDs
}

func (h *Handler) resolveChatPriorSession(r *http.Request, task *db.AgentTaskQueue, cs db.ChatSession, resp *AgentTaskResponse) {
	if !task.ForceFreshSession {
		if cs.SessionID.Valid && cs.RuntimeID.Valid && cs.RuntimeID == task.RuntimeID {
			resp.PriorSessionID = cs.SessionID.String
		}
		if cs.WorkDir.Valid {
			resp.PriorWorkDir = cs.WorkDir.String
		}
	}
}

func (h *Handler) resolveChatSessionFallback(r *http.Request, task *db.AgentTaskQueue, cs db.ChatSession, resp *AgentTaskResponse) {
	if task.ForceFreshSession {
		return
	}
	if chatSessionResumeFallbackNeeded(resp.PriorSessionID, resp.PriorWorkDir) {
		h.Metrics.RecordChatClaimSessionFallbackNeeded()
		started := time.Now()
		prior, err := h.Queries.GetLastChatTaskSession(r.Context(), cs.ID)
		h.Metrics.ObserveChatClaimLastSessionQuery(time.Since(started).Seconds())
		switch {
		case err == nil && prior.SessionID.Valid:
			h.Metrics.RecordChatClaimSessionFallbackHit()
			if resp.PriorSessionID == "" && prior.RuntimeID == task.RuntimeID {
				resp.PriorSessionID = prior.SessionID.String
			}
			if prior.WorkDir.Valid && resp.PriorWorkDir == "" {
				resp.PriorWorkDir = prior.WorkDir.String
			}
		case errors.Is(err, pgx.ErrNoRows):
			h.Metrics.RecordChatClaimSessionFallbackMiss()
		case err == nil:
			h.Metrics.RecordChatClaimSessionFallbackMiss()
		default:
			h.Metrics.RecordChatClaimSessionFallbackError()
		}
	}
	started := time.Now()
	missing, err := h.Queries.GetLatestChatTaskRolloutMissing(r.Context(), cs.ID)
	h.Metrics.ObserveChatClaimRolloutMissingQuery(time.Since(started).Seconds())
	if err == nil && missing {
		resp.PriorSessionResumeUnavailable = true
	}
}

func (h *Handler) resolveChatInputMessages(r *http.Request, task *db.AgentTaskQueue, cs db.ChatSession, resp *AgentTaskResponse) *claimBuildFailure {
	var unanswered []db.ChatMessage
	var inputLoadErr error
	if task.ChatInputTaskID.Valid {
		unanswered, inputLoadErr = h.Queries.ListChatInputMessages(r.Context(), task.ChatInputTaskID)
	} else if msgs, err := h.Queries.ListChatMessagesForLegacyTask(r.Context(), cs.ID); err == nil {
		unanswered = trailingUserMessages(msgs)
	} else {
		inputLoadErr = err
	}
	if inputLoadErr != nil {
		slog.Error("chat claim: load chat input messages failed; preserving task for redelivery",
			"task_id", uuidToString(task.ID),
			"chat_session_id", uuidToString(cs.ID),
			"error", inputLoadErr)
		return &claimBuildFailure{
			outcome: "error_chat_input_load",
			status:  http.StatusInternalServerError,
			message: "failed to load chat input",
		}
	}

	h.resolveChatSessionFallback(r, task, cs, resp)

	parts := make([]string, 0, len(unanswered))
	for _, m := range unanswered {
		if strings.TrimSpace(m.Content) != "" {
			parts = append(parts, m.Content)
		}
		if atts, attErr := h.Queries.ListAttachmentsByChatMessage(r.Context(), db.ListAttachmentsByChatMessageParams{
			ChatMessageID: m.ID,
			WorkspaceID:   parseUUID(resp.WorkspaceID),
		}); attErr == nil && len(atts) > 0 {
			for _, a := range atts {
				resp.ChatMessageAttachments = append(resp.ChatMessageAttachments, ChatAttachmentMeta{
					ID:          uuidToString(a.ID),
					Filename:    a.Filename,
					ContentType: a.ContentType,
				})
			}
		}
	}
	resp.ChatMessage = strings.Join(parts, "\n\n")

	if task.ChatInputTaskID.Valid && !resp.ChatIntro && strings.TrimSpace(resp.ChatMessage) == "" {
		slog.Error("chat claim: task-owned direct task has no user input; cancelling",
			"task_id", uuidToString(task.ID),
			"chat_session_id", uuidToString(cs.ID),
			"chat_input_task_id", uuidToString(task.ChatInputTaskID),
		)
		if _, cerr := h.TaskService.CancelTask(r.Context(), task.ID); cerr != nil {
			slog.Error("chat claim: cancel after empty input failed",
				"task_id", uuidToString(task.ID), "error", cerr)
		}
		return &claimBuildFailure{
			outcome: "error_empty_chat_input",
			status:  http.StatusInternalServerError,
			message: "chat task has no user input",
		}
	}

	if strings.TrimSpace(resp.ThreadName) == "" && resp.ChatMessage != "" {
		resp.ThreadName = resp.ChatMessage
	}
	return nil
}

func (h *Handler) resolveClaimChatContext(r *http.Request, task *db.AgentTaskQueue, resp *AgentTaskResponse) *claimBuildFailure {
	cs, err := h.Queries.GetChatSession(r.Context(), task.ChatSessionID)
	if err != nil {
		return nil
	}
	resp.WorkspaceID = uuidToString(cs.WorkspaceID)
	resp.ChatSessionID = uuidToString(cs.ID)
	resp.ThreadName = cs.Title
	if cs.IsAgentIntro {
		if hasUser, herr := h.Queries.ChatSessionHasUserMessage(r.Context(), cs.ID); herr != nil {
			slog.Warn("chat intro gate: has-user-message check failed",
				"chat_session_id", uuidToString(cs.ID), "error", herr)
		} else {
			resp.ChatIntro = !hasUser
		}
	}
	if binding, berr := h.Queries.GetChannelChatSessionBindingBySessionAny(r.Context(), cs.ID); berr == nil {
		resp.ChatChannelType = binding.ChannelType
		resp.ChatType = binding.ChatType
		resp.ChatChannelDeliversFiles = h.channelDeliversFiles(binding.ChannelType)
		if binding.ChannelType == string(slack.TypeSlack) {
			resp.ChatInThread = binding.LastThreadID.Valid && binding.LastThreadID.String != "" &&
				binding.LastThreadID.String != binding.LastMessageID.String
		}
	}
	var projectRepos []RepoData
	if cs.ProjectID.Valid {
		if project, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
			ID:          cs.ProjectID,
			WorkspaceID: cs.WorkspaceID,
		}); err == nil {
			resp.ProjectID = uuidToString(project.ID)
			resp.ProjectTitle = project.Title
			resp.ProjectDescription = project.Description.String
			resources, repos := h.resolveProjectResourcesAndRepos(r.Context(), project.ID)
			if len(resources) > 0 {
				resp.ProjectResources = resources
				projectRepos = repos
			}
		}
	}
	if len(projectRepos) > 0 {
		resp.Repos = projectRepos
	} else if ws, err := h.Queries.GetWorkspace(r.Context(), cs.WorkspaceID); err == nil && ws.Repos != nil {
		var repos []RepoData
		if json.Unmarshal(ws.Repos, &repos) == nil && len(repos) > 0 {
			resp.Repos = repos
		}
	}

	h.resolveChatPriorSession(r, task, cs, resp)
	return h.resolveChatInputMessages(r, task, cs, resp)
}

func (h *Handler) resolveClaimAutopilotContext(ctx context.Context, task *db.AgentTaskQueue, resp *AgentTaskResponse) {
	if run, err := h.Queries.GetAutopilotRun(ctx, task.AutopilotRunID); err == nil {
		resp.AutopilotID = uuidToString(run.AutopilotID)
		resp.AutopilotSource = run.Source
		if run.TriggerPayload != nil {
			resp.AutopilotTriggerPayload = json.RawMessage(run.TriggerPayload)
		}
		if ap, err := h.Queries.GetAutopilot(ctx, run.AutopilotID); err == nil {
			resp.AutopilotTitle = ap.Title
			resp.ThreadName = ap.Title
			if ap.Description.Valid {
				resp.AutopilotDescription = ap.Description.String
			}
			if resp.WorkspaceID == "" {
				resp.WorkspaceID = uuidToString(ap.WorkspaceID)
			}
			if len(resp.Repos) == 0 {
				if ws, err := h.Queries.GetWorkspace(ctx, ap.WorkspaceID); err == nil && ws.Repos != nil {
					var repos []RepoData
					if json.Unmarshal(ws.Repos, &repos) == nil && len(repos) > 0 {
						resp.Repos = repos
					}
				}
			}
		}
	}
}

func (h *Handler) resolveClaimQuickCreateContext(r *http.Request, task *db.AgentTaskQueue, resp *AgentTaskResponse) bool {
	var qc service.QuickCreateContext
	if json.Unmarshal(task.Context, &qc) != nil || qc.Type != service.QuickCreateContextType {
		return false
	}
	resp.QuickCreatePrompt = qc.Prompt
	resp.QuickCreatePriority = qc.Priority
	resp.QuickCreateDueDate = qc.DueDate
	resp.QuickCreateAttachmentIDs = append([]string(nil), qc.AttachmentIDs...)
	resp.ThreadName = qc.Prompt
	resp.WorkspaceID = qc.WorkspaceID

	var projectRepos []RepoData
	if qc.ProjectID != "" {
		if projectUUID, err := util.ParseUUID(qc.ProjectID); err == nil {
			resp.ProjectID = qc.ProjectID
			if proj, err := h.Queries.GetProject(r.Context(), projectUUID); err == nil {
				resp.ProjectTitle = proj.Title
				resp.ProjectDescription = proj.Description.String
			}
			resources, repos := h.resolveProjectResourcesAndRepos(r.Context(), projectUUID)
			if len(resources) > 0 {
				resp.ProjectResources = resources
				projectRepos = repos
			}
		}
	}

	if len(projectRepos) > 0 {
		resp.Repos = projectRepos
	} else if ws, err := h.Queries.GetWorkspace(r.Context(), parseUUID(qc.WorkspaceID)); err == nil && ws.Repos != nil {
		var repos []RepoData
		if json.Unmarshal(ws.Repos, &repos) == nil && len(repos) > 0 {
			resp.Repos = repos
		}
	}

	if qc.ParentIssueID != "" {
		resp.ParentIssueID = qc.ParentIssueID
		if parentUUID, err := util.ParseUUID(qc.ParentIssueID); err == nil {
			if wsUUID, wsErr := util.ParseUUID(qc.WorkspaceID); wsErr == nil {
				parent, perr := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
					ID:          parentUUID,
					WorkspaceID: wsUUID,
				})
				if perr == nil && parent.ID.Valid {
					if ws, werr := h.Queries.GetWorkspace(r.Context(), wsUUID); werr == nil {
						resp.ParentIssueIdentifier = ws.IssuePrefix + "-" + strconv.Itoa(int(parent.Number))
					}
				}
			}
		}
	}

	h.resolveQuickCreateSquadBriefing(r.Context(), qc, resp)
	return true
}

func (h *Handler) verifyClaimIsolationAndWorktree(r *http.Request, task *db.AgentTaskQueue, runtime db.AgentRuntime, runtimeID, runtimeWorkspaceID string, hasQuickCreate bool, resp *AgentTaskResponse) *claimBuildFailure {
	if resp.WorkspaceID == "" || resp.WorkspaceID != runtimeWorkspaceID {
		slog.Error("task claim: workspace isolation check failed, cancelling task",
			"task_id", uuidToString(task.ID),
			"runtime_id", runtimeID,
			"runtime_workspace", runtimeWorkspaceID,
			"resolved_workspace", resp.WorkspaceID,
			"has_issue", task.IssueID.Valid,
			"has_chat", task.ChatSessionID.Valid,
			"has_autopilot_run", task.AutopilotRunID.Valid,
			"has_quick_create", hasQuickCreate,
		)
		if _, cerr := h.TaskService.CancelTask(r.Context(), task.ID); cerr != nil {
			slog.Error("task claim: cancel after workspace check failed",
				"task_id", uuidToString(task.ID), "error", cerr)
		}
		return &claimBuildFailure{
			outcome: "error_workspace",
			status:  http.StatusInternalServerError,
			message: "task workspace isolation check failed",
		}
	}

	if siblings, err := h.Queries.ListActiveSiblingIssueTasks(r.Context(), db.ListActiveSiblingIssueTasksParams{
		AgentID:     task.AgentID,
		TaskID:      task.ID,
		WorkspaceID: parseUUID(resp.WorkspaceID),
	}); err == nil {
		resp.ActiveSiblingRuns = make([]ActiveSiblingRunData, 0, len(siblings))
		for _, sibling := range siblings {
			resp.ActiveSiblingRuns = append(resp.ActiveSiblingRuns, ActiveSiblingRunData{
				TaskID:          uuidToString(sibling.TaskID),
				IssueID:         uuidToString(sibling.IssueID),
				IssueIdentifier: fmt.Sprintf("%s-%d", sibling.IssuePrefix, sibling.IssueNumber),
				IssueTitle:      sibling.IssueTitle,
				Status:          sibling.Status,
				CreatedAt:       timestampToString(sibling.CreatedAt),
				StartedAt:       timestampToString(sibling.StartedAt),
			})
		}
	} else {
		slog.Warn("task claim: failed to load active sibling runs",
			"task_id", uuidToString(task.ID),
			"agent_id", uuidToString(task.AgentID),
			"error", err,
		)
	}

	if ws, err := h.Queries.GetWorkspace(r.Context(), parseUUID(resp.WorkspaceID)); err == nil {
		if ws.Context.Valid {
			resp.WorkspaceContext = ws.Context.String
		}
	} else {
		slog.Warn("task claim: failed to load workspace for context injection",
			"task_id", uuidToString(task.ID),
			"workspace_id", resp.WorkspaceID,
			"error", err,
		)
	}

	if reason := worktreeClaimBlockReason(
		resp.ProjectResources,
		runtime,
		requestHasClientCapability(r, protocol.DaemonCapabilityLocalWorktreeV1),
	); reason != "" {
		slog.Error("task claim: runtime too old for worktree mode; cancelling rather than running in place",
			"task_id", uuidToString(task.ID),
			"runtime_id", runtimeID,
			"daemon_id", runtime.DaemonID.String,
			"reason", reason,
		)
		if _, cerr := h.TaskService.CancelTaskWithReason(r.Context(), task.ID, reason, "local_directory_error"); cerr != nil {
			slog.Error("task claim: cancel after worktree version gate failed; requeueing so the gate can run again",
				"task_id", uuidToString(task.ID), "error", cerr)
			if _, rerr := h.TaskService.RequeueTaskAfterClaimFailure(r.Context(), *task); rerr != nil {
				slog.Error("task claim: requeue after worktree-gate cancel failure failed; stale reclaim will recover it",
					"task_id", uuidToString(task.ID), "error", rerr)
			}
			return &claimBuildFailure{
				outcome: "error_worktree_gate_cancel",
				status:  http.StatusInternalServerError,
				message: "failed to cancel a worktree task blocked by daemon version; task requeued",
			}
		}
		return &claimBuildFailure{
			outcome: "error_worktree_daemon_version",
			status:  http.StatusUnprocessableEntity,
			message: reason,
		}
	}

	return nil
}

func (h *Handler) resolveProjectResourcesAndRepos(ctx context.Context, projectID pgtype.UUID) ([]ProjectResourceData, []RepoData) {
	rows := h.listProjectResourcesForProject(ctx, projectID)
	if len(rows) == 0 {
		return nil, nil
	}
	resources := make([]ProjectResourceData, 0, len(rows))
	var repos []RepoData
	for _, row := range rows {
		label := ""
		if row.Label.Valid {
			label = row.Label.String
		}
		ref := json.RawMessage(row.ResourceRef)
		if len(ref) == 0 {
			ref = json.RawMessage("{}")
		}
		resources = append(resources, ProjectResourceData{
			ID:           uuidToString(row.ID),
			ResourceType: row.ResourceType,
			ResourceRef:  ref,
			Label:        label,
		})
		if row.ResourceType == "github_repo" {
			var payload struct {
				URL string `json:"url"`
				Ref string `json:"ref,omitempty"`
			}
			if json.Unmarshal(row.ResourceRef, &payload) == nil && payload.URL != "" {
				repos = append(repos, RepoData{URL: payload.URL, Ref: strings.TrimSpace(payload.Ref)})
			}
		}
	}
	return resources, repos
}

func (h *Handler) resolveQuickCreateSquadBriefing(ctx context.Context, qc service.QuickCreateContext, resp *AgentTaskResponse) {
	if resp.Agent == nil || qc.SquadID == "" {
		return
	}
	wsUUID, wsErr := util.ParseUUID(qc.WorkspaceID)
	squadUUID, sqErr := util.ParseUUID(qc.SquadID)
	if wsErr != nil || sqErr != nil {
		return
	}
	squad, err := h.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
		ID:          squadUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil || uuidToString(squad.LeaderID) != resp.Agent.ID {
		return
	}
	briefing := buildSquadLeaderBriefing(ctx, h.Queries, squad, false)
	if strings.TrimSpace(resp.Agent.Instructions) == "" {
		resp.Agent.Instructions = briefing
	} else {
		resp.Agent.Instructions = resp.Agent.Instructions + "\n\n" + briefing
	}
	resp.SquadID = uuidToString(squad.ID)
	resp.SquadName = squad.Name
	slog.Debug("injected squad leader briefing for quick-create",
		"squad_id", uuidToString(squad.ID),
		"squad_name", squad.Name,
		"leader_agent_id", resp.Agent.ID,
	)
}
