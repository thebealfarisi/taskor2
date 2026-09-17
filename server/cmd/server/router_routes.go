package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/storage"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func mountAllRoutes(
	r chi.Router,
	h *handler.Handler,
	health *serverHealth,
	store storage.Storage,
	hub *realtime.Hub,
	queries *db.Queries,
	patCache *auth.PATCache,
	daemonTokenCache *auth.DaemonTokenCache,
	cloudPATVerifier *auth.CloudPATVerifier,
	rdb *redis.Client,
) {
	mountPublicAndHealthRoutes(r, h, health, store, hub, queries, patCache, rdb)
	mountDaemonAPIRoutes(r, h, queries, patCache, daemonTokenCache, cloudPATVerifier)
	mountProtectedRoutes(r, h, queries, patCache, cloudPATVerifier, rdb)
}

func mountPublicAndHealthRoutes(
	r chi.Router,
	h *handler.Handler,
	health *serverHealth,
	store storage.Storage,
	hub *realtime.Hub,
	queries *db.Queries,
	patCache *auth.PATCache,
	rdb *redis.Client,
) {
	// Health / readiness checks
	r.Get("/health", health.liveHandler)
	r.Get("/readyz", health.readyHandler)
	r.Get("/healthz", health.readyHandler)

	// Realtime subsystem metrics — connection counts, slow-client evictions,
	// and per-event-type send QPS counters. Exposed as JSON so it can be
	// scraped by ops or surfaced in the admin UI without adding a Prometheus
	// dependency. See MUL-1138 (Phase 0).
	//
	// Access is restricted (MUL-1342): when REALTIME_METRICS_TOKEN is set,
	// callers must present it via Authorization: Bearer <token>. When the
	// env var is unset the handler only serves loopback callers so local
	// dev keeps working without exposing the metrics on a public listener.
	r.Get("/health/realtime", realtimeMetricsHandler(os.Getenv("REALTIME_METRICS_TOKEN")))

	// WebSocket
	mc := &membershipChecker{queries: queries}
	pr := &patResolver{queries: queries, cache: patCache}
	slugResolver := realtime.SlugResolver(func(ctx context.Context, slug string) (string, error) {
		ws, err := queries.GetWorkspaceBySlug(ctx, slug)
		if err != nil {
			return "", err
		}
		return util.UUIDToString(ws.ID), nil
	})
	r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		realtime.HandleWebSocket(hub, mc, pr, slugResolver, w, r)
	})

	// Local file serving (when using local storage). Served through the
	// handler so /uploads/* carries the same preview security headers as the
	// /api/attachments download endpoint; self-hosted split-origin/same-origin
	// clients can then iframe-preview PDFs/HTML fetched straight from the
	// static route instead of hitting the global frame-ancestors 'none' CSP.
	// See MUL-3821 / #4477.
	if _, ok := store.(*storage.LocalStorage); ok {
		r.Get("/uploads/*", h.ServeLocalUpload)
	}

	// Capability-authenticated attachment download (MUL-5292). Public by
	// necessity: a native download (Electron's webContents.downloadURL, a
	// cross-site webview <img>) carries neither Authorization nor a session
	// cookie, so there is nothing here for middleware.Auth to read. The
	// short-lived, single-attachment signature in the query is the credential,
	// and it is only ever minted by the AUTHENTICATED GET
	// /api/attachments/{id} after that request's membership check passed.
	// The authenticated /api/attachments/{id}/download route below is
	// unchanged — this one is purely additive.
	r.Get("/api/attachments/{id}/signed-download", h.DownloadAttachmentWithCapability)

	// Avatar serving. Public for the same reason as the capability download
	// above: the auth cookie is SameSite=Strict, so an auth-gated URL cannot
	// be a native <img src> from Desktop / mobile webview or a split-origin
	// self-hosted web app. The HMAC signature in the path is the credential.
	// It covers the storage key, only image keys resolve, and the object must
	// be avatar-class — see server/internal/handler/avatar.go (MUL-5393 /
	// #6024).
	r.Get("/api/avatars/{sig}/*", h.ServeAvatar)

	// Auth (public) — per-IP rate limiting.
	if rdb == nil {
		slog.Warn("auth rate limiting disabled: REDIS_URL not configured")
	}
	trustedProxies := middleware.ParseTrustedProxies(os.Getenv("RATE_LIMIT_TRUSTED_PROXIES"))
	authRL := middleware.RateLimit(rdb, envPositiveInt("RATE_LIMIT_AUTH", 5), time.Minute, trustedProxies)
	authVerifyRL := middleware.RateLimit(rdb, envPositiveInt("RATE_LIMIT_AUTH_VERIFY", 20), time.Minute, trustedProxies)
	contactSalesRL := middleware.RateLimit(rdb, envPositiveInt("RATE_LIMIT_CONTACT_SALES", 5), time.Hour, trustedProxies)
	r.With(authRL).Post("/auth/send-code", h.SendCode)
	r.With(authVerifyRL).Post("/auth/verify-code", h.VerifyCode)
	r.With(authRL).Post("/auth/google", h.GoogleLogin)
	r.Post("/auth/logout", h.Logout)
	// Keycloak SSO (public — handle the auth flow itself; 404 when disabled)
	r.With(authRL).Get("/auth/keycloak/login", h.KeycloakLogin)
	r.With(authRL).Get("/auth/keycloak/callback", h.KeycloakCallback)
	r.Get("/auth/keycloak/logout", h.KeycloakLogout)

	// Public API
	r.Get("/api/config", h.GetConfig)
	r.With(contactSalesRL).Post("/api/contact-sales", h.CreateContactSales)
	// Public share-link preview — no auth: shows the workspace name/slug and
	// inviter so a not-yet-logged-in visitor can see what they're joining.
	r.Get("/api/share-links/{code}", h.GetShareLinkInfo)

	// Webhook ingress for autopilots. Outside the authenticated group on
	// purpose: the bearer token in the URL path IS the credential. Workspace
	// context is derived from the trigger row, never from request headers.
	r.Post("/api/webhooks/autopilots/{token}", h.HandleAutopilotWebhook)
	// GitHub App webhook (no Multica auth — requests are authenticated via
	// HMAC-SHA256 signature in the handler) and post-install setup callback.
	r.Post("/api/webhooks/github", h.HandleGitHubWebhook)
	r.Get("/api/github/setup", h.GitHubSetupCallback)
	// Slack OAuth callback (no Multica auth in the path — it is hit by Slack's
	// browser redirect; the workspace/agent/initiator are recovered from the
	// sealed state). It exchanges the code, upserts the install, then bounces
	// the browser back to Settings → Integrations.
	// VCS webhook for token-based providers (Forgejo / Gitea / GitLab). No Multica
	// auth — authenticated per-connection by the provider's signature scheme;
	// the connection id in the path selects the workspace, provider, and
	// decryption secret.
	r.Post("/api/webhooks/vcs/{connectionId}", h.HandleVCSWebhook)
	// Stripe webhook (no Multica auth — Stripe signs the raw body
	// with a shared secret, the multica-cloud upstream verifies. We
	// only forward the bytes + the Stripe-Signature header; see
	// HandleCloudBillingStripeWebhook for the rationale).
	r.Post("/api/webhooks/stripe", h.HandleCloudBillingStripeWebhook)

	// Composio OAuth callback (MUL-3843). NOT under the Auth group on purpose:
	// Composio 302-redirects the user's browser here at the end of the OAuth
	// flow, and the cookie session is frequently absent (expired session,
	// SameSite=Strict / Safari ITP stripping cross-site cookies, private
	// windows, self-hosted callbacks on a different subdomain). Identity is NOT
	// taken from the session — it comes from the HMAC-signed `state` query
	// param, which CompleteCallback verifies (signature, expiry, replay) before
	// doing anything. h.Composio == nil still returns 503. Keeping it inside the
	// Auth group made a missing cookie a hard 401, breaking the flow for exactly
	// the browsers above; the other four composio endpoints stay session-gated.
	r.Get("/api/integrations/composio/callback", h.ComposioCallback)

}

func mountDaemonAPIRoutes(
	r chi.Router,
	h *handler.Handler,
	queries *db.Queries,
	patCache *auth.PATCache,
	daemonTokenCache *auth.DaemonTokenCache,
	cloudPATVerifier *auth.CloudPATVerifier,
) {
	// Daemon API routes (require daemon token or valid user token)
	r.Route("/api/daemon", func(r chi.Router) {
		r.Use(middleware.DaemonAuth(queries, patCache, daemonTokenCache, cloudPATVerifier))

		r.Post("/register", h.DaemonRegister)
		r.Post("/deregister", h.DaemonDeregister)
		r.Post("/heartbeat", h.DaemonHeartbeat)
		r.Get("/ws", h.DaemonWebSocket)
		r.Get("/workspaces", h.ListDaemonWorkspaces)
		r.Get("/workspaces/{workspaceId}/repos", h.GetDaemonWorkspaceRepos)
		r.Get("/workspaces/{workspaceId}/runtime-profiles", h.DaemonListRuntimeProfiles)

		// Agent-triggered plugin hooks. The daemon's local MCP server calls
		// this when an agent picks one of its tools; the server makes the
		// signed request so the daemon never holds the signing secret.
		r.Post("/tasks/{id}/plugin-hooks", h.InvokeAgentPluginHook)
		// The broker asks for an mcp hook's credential at connection time, so
		// a secret never sits in a task record.
		r.Get("/tasks/{id}/plugin-mcp/{contributionId}/credential", h.ResolvePluginMCPCredential)

		r.Post("/runtimes/{runtimeId}/tasks/claim", h.ClaimTaskByRuntime)
		// Canonical machine-level batch claim (MUL-4257). `/claim` is a
		// transitional alias; the daemon coordinator targets the canonical
		// path.
		r.Post("/tasks/claim", h.ClaimTasksByRuntime)
		r.Post("/claim", h.ClaimTasksByRuntime)
		r.Post("/runtimes/{runtimeId}/tasks/{taskId}/prepare-lease", h.ExtendTaskPrepareLease)
		r.Post("/runtimes/{runtimeId}/tasks/{taskId}/skill-bundles/resolve", h.ResolveTaskSkillBundles)
		r.Get("/runtimes/{runtimeId}/tasks/pending", h.ListPendingTasksByRuntime)
		r.Post("/runtimes/{runtimeId}/update/{updateId}/result", h.ReportUpdateResult)
		r.Post("/runtimes/{runtimeId}/models/{requestId}/result", h.ReportModelListResult)
		r.Post("/runtimes/{runtimeId}/local-skills/{requestId}/result", h.ReportLocalSkillListResult)
		r.Post("/runtimes/{runtimeId}/local-skills/import/{requestId}/result", h.ReportLocalSkillImportResult)

		r.Get("/tasks/{taskId}/status", h.GetTaskStatus)
		r.Post("/tasks/{taskId}/start", h.StartTask)
		r.Post("/tasks/{taskId}/wait-local-directory", h.MarkTaskWaitingLocalDirectory)
		r.Post("/tasks/{taskId}/progress", h.ReportTaskProgress)
		r.Post("/tasks/{taskId}/complete", h.CompleteTask)
		r.Post("/tasks/{taskId}/fail", h.FailTask)
		r.Post("/tasks/{taskId}/usage", h.ReportTaskUsage)
		r.Post("/tasks/{taskId}/messages", h.ReportTaskMessages)
		r.Get("/tasks/{taskId}/messages", h.ListTaskMessages)
		r.Post("/tasks/{taskId}/cancel-ack", h.AckTaskCancelled)

		r.Post("/workspaces/{workspaceId}/issues/gc-check", h.BatchIssueGCCheck)
		r.Get("/issues/{issueId}/gc-check", h.GetIssueGCCheck)
		r.Get("/chat-sessions/{sessionId}/gc-check", h.GetChatSessionGCCheck)
		r.Get("/autopilot-runs/{runId}/gc-check", h.GetAutopilotRunGCCheck)
		r.Get("/tasks/{taskId}/gc-check", h.GetTaskGCCheck)

		r.Post("/runtimes/{runtimeId}/recover-orphans", h.RecoverOrphanedTasks)
		r.Post("/tasks/{taskId}/session", h.PinTaskSession)
	})
}

func mountPluginAPIRoutes(
	r chi.Router,
	h *handler.Handler,
	queries *db.Queries,
	patCache *auth.PATCache,
	cloudPATVerifier *auth.CloudPATVerifier,
) {
	r.Group(func(r chi.Router) {
		r.Use(middleware.PluginAuth(middleware.Auth(queries, patCache, cloudPATVerifier)))

		r.Route("/api/v1/plugin", func(r chi.Router) {
			r.Get("/context", h.GetPluginContext)
			r.Get("/issues/{id}", h.GetPluginIssue)
			r.Patch("/issues/{id}", h.PatchPluginIssue)
			r.Get("/issues/{id}/comments", h.ListPluginComments)
			r.Post("/issues/{id}/comments", h.CreatePluginComment)
			r.Get("/storage/{scope}", h.ListPluginStorage)
			r.Get(routeStorageScopeKey, h.GetPluginStorage)
			r.Put(routeStorageScopeKey, h.PutPluginStorage)
			r.Delete(routeStorageScopeKey, h.DeletePluginStorage)
			// ui / manual only. `event` is dispatched by the host off the event
			// bus and never requested; `agent` arrives over MCP in PR 4.
			r.Post("/hooks/{key}", h.InvokePluginHook)
		})
	})
}

func mountProtectedRoutes(
	r chi.Router,
	h *handler.Handler,
	queries *db.Queries,
	patCache *auth.PATCache,
	cloudPATVerifier *auth.CloudPATVerifier,
	rdb *redis.Client,
) {
	mountPluginAPIRoutes(r, h, queries, patCache, cloudPATVerifier)

	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(queries, patCache, cloudPATVerifier))
		r.Use(middleware.RefreshCloudFrontCookies(h.CFSigner))

		mountUserScopedRoutes(r, h, queries)
		mountWorkspaceScopedRoutes(r, h, queries)
	})
}

func mountUserScopedRoutes(
	r chi.Router,
	h *handler.Handler,
	queries *db.Queries,
) {
	// --- User-scoped routes (no workspace context required) ---
	r.Get("/api/me", h.GetMe)
	r.Patch("/api/me", h.UpdateMe)
	r.Patch("/api/me/onboarding", h.PatchOnboarding)
	r.Post("/api/me/onboarding/complete", h.CompleteOnboarding)
	r.Post("/api/me/onboarding/cloud-waitlist", h.JoinCloudWaitlist)
	// DEPRECATED — shim routes for desktop < v3 during the rollout
	// window. v3 frontend creates the Helper agent + starter issue
	// via generic CreateAgent / CreateIssue and only calls /complete
	// here. Remove once X-Client-Version telemetry confirms zero
	// pre-v3 desktops are still calling these. Handlers live in
	// server/internal/handler/onboarding_shim.go.
	r.Post("/api/me/onboarding/runtime-bootstrap", h.BootstrapOnboardingRuntime)
	r.Post("/api/me/onboarding/no-runtime-bootstrap", h.BootstrapOnboardingNoRuntime)
	r.Post("/api/cli-token", h.IssueCliToken)
	r.Post("/api/upload-file", h.UploadFile)
	r.Post("/api/feedback", h.CreateFeedback)
	r.With(handler.RequireHumanActor).Post("/api/client-usage", h.UpsertClientUsage)

	// Note (MUL-4309): the generic OpenAI-compatible passthrough endpoints
	// (POST /api/llm/v1/chat/completions[/stream]) were intentionally
	// removed. Exposing a general LLM proxy backed by the deployment's own
	// key let any logged-in user run arbitrary completions on our dime.
	// LLM access is now server-internal only (see pkg/llm); anything the
	// web/client needs must go through a purpose-built business endpoint
	// that fixes the prompt/model server-side (e.g. chat title generation).

	// Attachment download — user-scoped (auth-only), NOT
	// workspace-scoped. The handler self-resolves the workspace
	// from the attachment row and enforces membership inside, so
	// this route is callable as a native browser <img>/<video>
	// src that cannot attach X-Workspace-Slug / X-Workspace-ID
	// headers. Persisting `/api/attachments/<id>/download` into
	// comment markdown depends on this — see MUL-3130. The
	// metadata / delete endpoints below stay workspace-scoped
	// because they are JSON-API consumers that always have
	// workspace context.
	r.Get("/api/attachments/{id}/download", h.DownloadAttachment)

	r.Route("/api/workspaces", func(r chi.Router) {
		r.Get("/", h.ListWorkspaces)
		r.Post("/", h.CreateWorkspace)
		r.Route("/{id}", func(r chi.Router) {
			// Member-level access
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireWorkspaceMemberFromURL(queries, "id"))
				r.Get("/", h.GetWorkspace)
				r.Get(routeMembers, h.ListMembersWithUser)
				r.Post("/leave", h.LeaveWorkspace)
				r.Get("/invitations", h.ListWorkspaceInvitations)
				// Listing GitHub installations is member-visible so the
				// integrations tab no longer renders blank for non-admins;
				// the handler strips the management handle and adds a
				// can_manage hint so the UI can gate connect/disconnect.
				r.Get("/github/installations", h.ListGitHubInstallations)
				// VCS connections (Forgejo / Gitea / GitLab) — member-visible
				// for the same reason as GitHub installations; connect /
				// disconnect are admin-gated in the group below.
				r.Get("/vcs/connections", h.ListVCSConnections)
				// Custom runtime profiles — listing/reading is member-visible
				// (the Runtime page renders for everyone; create/edit/delete
				// are admin-gated below).
				r.Get("/runtime-profiles", h.ListRuntimeProfiles)
				r.Get(routeRuntimeProfilesWithID, h.GetRuntimeProfile)
				// The workspace MCP library — member-visible so an agent
				// owner can see what is available to add to their agent.
				// The payload is names and transports only; the stored
				// entries are write-only.
				r.Get(routeMCPServers, h.ListWorkspaceMcpServers)
				// Installed Plugins are member-visible so a member can
				// see what is mounted in their workspace and which scopes
				// it holds; install / configure / remove stay admin-only.
				r.Get("/plugins", h.ListPlugins)
			})
			// Admin-level access
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireWorkspaceRoleFromURL(queries, "id", "owner", "admin"))
				r.Put("/", h.UpdateWorkspace)
				r.Patch("/", h.UpdateWorkspace)
				r.Post(routeMembers, h.CreateInvitation)
				r.Route("/members/{memberId}", func(r chi.Router) {
					r.Patch("/", h.UpdateMember)
					r.Delete("/", h.DeleteMember)
				})
				r.Delete("/invitations/{invitationId}", h.RevokeInvitation)
				// Curating the shared MCP library is an admin action.
				// Creating an entry binds it to no agent; an agent owner
				// adds it to their own agent through the agent routes.
				r.Post(routeMCPServers, h.CreateWorkspaceMcpServer)
				r.Put(routeMCPServersWithID, h.UpdateWorkspaceMcpServer)
				r.Delete(routeMCPServersWithID, h.DeleteWorkspaceMcpServer)
				r.Post("/share-links", h.CreateShareLink)
				r.Delete("/share-links/{linkId}", h.RevokeShareLink)
				r.Get("/share-links", h.ListShareLinks)
				// Custom runtime profile mutations (admin-only).
				r.Post("/runtime-profiles", h.CreateRuntimeProfile)
				r.Patch(routeRuntimeProfilesWithID, h.UpdateRuntimeProfile)
				r.Put(routeRuntimeProfilesWithID, h.UpdateRuntimeProfile)
				r.Delete(routeRuntimeProfilesWithID, h.DeleteRuntimeProfile)
				// Installing a Plugin is two steps on purpose: preview
				// parses the manifest and returns the scope list without
				// writing anything, so the consent screen has something to
				// show before an installation exists.
				r.Post("/plugins/preview", h.PreviewPlugin)
				r.Post("/plugins", h.InstallPlugin)
				r.Get("/plugins/{installationId}/invocations", h.ListPluginInvocations)
				r.Post("/plugins/{installationId}/token", h.RotatePluginToken)
				r.Delete("/plugins/{installationId}/token", h.RevokePluginToken)
				// mcp-transport approval. Discovery adopts nothing; the PUT
				// is the grant, and it pins the tools by schema digest.
				r.Get("/plugins/{installationId}/mcp/{hookKey}/tools", h.ListPluginMCPTools)
				r.Put("/plugins/{installationId}/mcp/{hookKey}/tools", h.ApprovePluginMCPTools)
				r.Put("/plugins/{installationId}/config", h.ConfigurePlugin)
				r.Post("/plugins/{installationId}/enable", h.EnablePlugin)
				r.Post("/plugins/{installationId}/disable", h.DisablePlugin)
				r.Delete("/plugins/{installationId}", h.UninstallPlugin)
			})
			// Owner-only access
			r.With(middleware.RequireWorkspaceRoleFromURL(queries, "id", "owner")).Delete("/", h.DeleteWorkspace)

			// GitHub integration — connect / disconnect remain admin-only;
			// the read-only list endpoint lives in the member-level group
			// above so non-admins can see the workspace's connection state.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireWorkspaceRoleFromURL(queries, "id", "owner", "admin"))
				r.Get("/github/connect", h.GitHubConnect)
				r.Get("/github/installations/{installationId}/repositories", h.ListGitHubInstallationRepositories)
				r.Delete("/github/installations/{installationId}", h.DeleteGitHubInstallation)
				// VCS connect / disconnect / webhook regeneration (admin-only).
				r.Post("/vcs/connections", h.ConnectVCS)
				r.Post("/vcs/connections/{connectionId}/rotate-webhook", h.RotateVCSConnectionWebhook)
				r.Delete("/vcs/connections/{connectionId}", h.DeleteVCSConnection)
			})

			// Lark integration. Every endpoint here only requires
			// workspace membership at the router; the real authorization
			// is per-agent and enforced inside each handler via
			// canManageAgent (agent owner OR workspace owner/admin), so an
			// agent's owner can bind/manage their own agent's Bot without
			// being a workspace admin (MUL-4213). The router can't make
			// that call itself: begin identifies the agent by an
			// `agent_id` query param and revoke by an installation id,
			// neither of which is a URL param the role middleware sees.
			//   - Listing stays member-visible (same rationale as GitHub:
			//     the Integrations tab must render for non-admins so they
			//     see "wired up by whom").
			//   - Begin / status / revoke each load the target agent and
			//     run canManageAgent (status gates on the session
			//     initiator or an admin) before doing anything.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireWorkspaceMemberFromURL(queries, "id"))
				r.Get("/lark/installations", h.ListLarkInstallations)
				r.Delete("/lark/installations/{installationId}", h.RevokeLarkInstallation)
				// Device-flow scan-to-install. Begin opens a new
				// registration session against Lark and returns
				// the QR-code URL; the frontend dialog then polls
				// /install/{sessionId}/status until success or
				// terminal failure.
				r.Post("/lark/install/begin", h.BeginLarkInstall)
				r.Get("/lark/install/{sessionId}/status", h.GetLarkInstallStatus)
			})

			// Slack integration (MUL-3666). Same admin/member split as
			// Lark: listing is member-visible; OAuth begin + revoke are
			// admin-only. The OAuth callback itself is a public route (it is
			// hit by Slack's browser redirect with no workspace in the path)
			// and is registered outside this workspace group.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireWorkspaceMemberFromURL(queries, "id"))
				r.Get("/slack/installations", h.ListSlackInstallations)
				r.Get("/wecom/installations", h.ListWecomInstallations)
			})
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireWorkspaceRoleFromURL(queries, "id", "owner", "admin"))
				r.Delete("/slack/installations/{installationId}", h.RevokeSlackInstallation)
				r.Post("/slack/install/byo", h.RegisterSlackBYO)
				r.Delete("/wecom/installations/{installationId}", h.RevokeWecomInstallation)
				r.Post("/wecom/install/byo", h.RegisterWecomBYO)
			})

			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireWorkspaceMemberFromURL(queries, "id"))
				r.Get("/dingtalk/installations", h.ListDingTalkInstallations)
				r.Get("/dingtalk/group-routes", h.ListDingTalkGroupRoutes)
			})
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireWorkspaceRoleFromURL(queries, "id", "owner", "admin"))
				r.Delete("/dingtalk/installations/{installationId}", h.RevokeDingTalkInstallation)
				r.Post("/dingtalk/install/byo", h.RegisterDingTalkBYO)
				r.Patch("/dingtalk/group-routes/{routeId}", h.UpdateDingTalkGroupRoute)
			})

			// Telegram integration. Same admin/member split as Slack:
			// listing is member-visible; install + revoke are admin-only.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireWorkspaceMemberFromURL(queries, "id"))
				r.Get("/telegram/installations", h.ListTelegramInstallations)
			})
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireWorkspaceRoleFromURL(queries, "id", "owner", "admin"))
				r.Delete("/telegram/installations/{installationId}", h.RevokeTelegramInstallation)
				r.Post("/telegram/install", h.RegisterTelegramBot)
			})
		})
	})

	// Lark binding-token redemption. NOT workspace-scoped because
	// the redeemer hits this BEFORE they have any workspace
	// context — the redemption itself is what mints their
	// lark_user_binding row. Identity comes from the session;
	// the token only proves "this open_id requested binding," and
	// is combined with the logged-in user to create the mapping.
	r.Post("/api/lark/binding/redeem", h.RedeemLarkBindingToken)
	// Slack binding-token redemption. Same rationale as Lark: NOT
	// workspace-scoped because the redeemer hits this before they have any
	// workspace context — the redemption itself mints their binding row. The
	// logged-in user (from the session) is bound to the Slack id the token
	// carries.
	r.Post("/api/slack/binding/redeem", h.RedeemSlackBindingToken)
	// DingTalk binding redemption is user-scoped for the same reason as
	// Slack: the token is redeemed before workspace context is selected.
	r.Post("/api/dingtalk/binding/redeem", h.RedeemDingTalkBindingToken)
	// WeCom smart-bot binding-token redemption. Same rationale as
	// Lark/Slack: the session is the source of truth for the redeemer's
	// Multica identity; the token only carries the WeCom userid to bind.
	r.Post("/api/wecom/binding/redeem", h.RedeemWecomBindingToken)
	// Telegram binding-token redemption. Same rationale: not
	// workspace-scoped, identity from the session, token proves only
	// "this Telegram user id requested binding".
	r.Post("/api/telegram/binding/redeem", h.RedeemTelegramBindingToken)

	// Composio integration (MUL-3720). User-scoped (no workspace context):
	// a connection belongs to a user. These four require a logged-in
	// session; the OAuth callback is the outlier and lives outside the Auth
	// group (registered above with the other public OAuth/webhook routes —
	// see MUL-3843). All return 503 when COMPOSIO_API_KEY is unset.
	r.Route("/api/integrations/composio", func(r chi.Router) {
		r.Post("/connect/init", h.ComposioConnectInit)
		r.Get("/toolkits", h.ListComposioToolkits)
		r.Get("/connections", h.ListComposioConnections)
		r.Delete("/connections/{id}", h.DeleteComposioConnection)
	})

	// User-scoped invitation routes (no workspace context required)
	r.Get("/api/invitations", h.ListMyInvitations)
	r.Get("/api/invitations/{id}", h.GetMyInvitation)
	r.Post("/api/invitations/{id}/accept", h.AcceptInvitation)
	r.Post("/api/invitations/{id}/decline", h.DeclineInvitation)
	r.Post("/api/share-links/join", h.JoinByShareLink)

	r.Route("/api/tokens", func(r chi.Router) {
		r.Get("/", h.ListPersonalAccessTokens)
		r.Post("/", h.CreatePersonalAccessToken)
		r.Post("/current/renew", h.RenewCurrentPersonalAccessToken)
		r.Delete("/{id}", h.RevokePersonalAccessToken)
	})

	// Cloud Billing proxy. Same upstream service / port as
	// cloud-runtime — multica-cloud's Fleet and Billing share
	// :8080 and the same chi router. All routes here forward
	// to /api/v1/billing/* with X-User-ID stamped from the
	// authenticated context.
	//
	// User-scoped (account-level), NOT workspace-scoped — sits
	// outside the RequireWorkspaceMember group so a user can
	// inspect their balance, top up, and open the Billing Portal
	// without an active workspace selected. The upstream owner
	// model is single-user; X-Workspace-ID would be ignored even
	// if we sent it. The Stripe webhook is the public outlier
	// and lives outside the entire Auth group (see above).
	//
	// IMPORTANT — task-token actors are blocked here. The Auth
	// middleware happily turns an mat_ task token into a normal
	// X-User-ID stamp (so agents can comment, claim issues, etc.
	// as their owner), but billing is account-level and a running
	// agent reading its owner's balance / opening a checkout
	// session is the kind of lateral-movement we're explicitly
	// trying to prevent. handler.RequireHumanActor checks the
	// authoritative server-set X-Actor-Source header and 403s
	// any task-token request. See actor_guards.go for the full
	// rationale.
	r.Route("/api/cloud-billing", func(r chi.Router) {
		r.Use(handler.RequireHumanActor)

		r.Get("/balance", h.GetCloudBillingBalance)
		r.Get("/transactions", h.ListCloudBillingTransactions)
		r.Get("/batches", h.ListCloudBillingBatches)
		r.Get("/topups", h.ListCloudBillingTopups)
		r.Get("/price-tiers", h.ListCloudBillingPriceTiers)
		r.Post("/checkout-sessions", h.CreateCloudBillingCheckoutSession)
		r.Get("/checkout-sessions/{sessionId}", h.GetCloudBillingCheckoutSession)
		r.Post("/portal-sessions", h.CreateCloudBillingPortalSession)
	})

	// Workspace subscriptions use the same cloud transport and Stripe
	// webhook as the existing owner-credit billing surface, but every request
	// is workspace-scoped. Entitlements, summary and prices are
	// member-readable; Checkout, seat reconcile, and Portal mutations require
	// owner/admin. The handlers also enforce
	// billing_workspace_subscriptions so a route refactor cannot
	// accidentally bypass the rollout flag.
	r.Route("/api/cloud-subscriptions", func(r chi.Router) {
		r.Use(handler.RequireHumanActor)

		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireWorkspaceMember(queries))
			r.Get("/entitlements", h.GetCloudWorkspaceEntitlements)
			r.Get("/summary", h.GetCloudWorkspaceSubscriptionSummary)
			r.Get("/prices", h.GetCloudWorkspaceSubscriptionPrices)
		})
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireWorkspaceRole(queries, "owner", "admin"))
			r.Post("/checkout-sessions", h.CreateCloudWorkspaceSubscriptionCheckout)
			r.Post("/seats/reconcile", h.ReconcileCloudWorkspaceSubscriptionSeats)
			r.Post("/portal-sessions", h.CreateCloudWorkspaceSubscriptionPortal)
		})
	})
}

func mountWorkspaceScopedRoutes(
	r chi.Router,
	h *handler.Handler,
	queries *db.Queries,
) {
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireWorkspaceMember(queries))

		mountWorkspaceIssueRoutes(r, h)
		mountWorkspaceProjectAndSquadRoutes(r, h)
		mountWorkspaceAgentAndChatRoutes(r, h)
		mountWorkspaceInboxAndCommentRoutes(r, h)
	})
}

func mountWorkspaceIssueRoutes(r chi.Router, h *handler.Handler) {
	// Assignee frequency
	r.Get("/api/assignee-frequency", h.GetAssigneeFrequency)

	// Issues
	r.Route("/api/issues", func(r chi.Router) {
		r.Post("/table/groups", h.ListIssueTableGroups)
		r.Post("/table/rows", h.ListIssueTableRows)
		r.Post("/table/facets", h.ListIssueTableFacets)
		r.Get(routeSearch, h.SearchIssues)
		r.Get("/child-progress", h.ChildIssueProgress)
		r.Get("/children", h.ListChildrenByParents)
		r.Get("/grouped", h.ListGroupedIssues)
		r.Get("/", h.ListIssues)
		// POST twin of GET /api/issues for oversized filter sets
		// (agents-working ids facet) — see QueryIssues.
		r.Post("/query", h.QueryIssues)
		r.Post("/", h.CreateIssue)
		r.Post("/quick-create", h.QuickCreateIssue)
		r.Post("/preview-trigger", h.PreviewIssueTrigger)
		r.Post("/batch-update", h.BatchUpdateIssues)
		r.Post("/batch-delete", h.BatchDeleteIssues)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.GetIssue)
			r.Put("/", h.UpdateIssue)
			r.Post("/move", h.MoveIssue)
			r.Delete("/", h.DeleteIssue)
			r.Post("/comments/trigger-preview", h.PreviewCommentTriggers)
			r.Post("/comments", h.CreateComment)
			r.Get("/comments", h.ListComments)
			r.Get("/timeline", h.ListTimeline)
			r.Get("/subscribers", h.ListIssueSubscribers)
			r.Post("/subscribe", h.SubscribeToIssue)
			r.Post("/unsubscribe", h.UnsubscribeFromIssue)
			r.Post("/unsubscribe/subtree", h.UnsubscribeFromIssueSubtree)
			r.Get("/active-task", h.GetActiveTaskForIssue)
			r.Post("/tasks/{taskId}/cancel", h.CancelTask)
			r.Post("/rerun", h.RerunIssue)
			r.Post("/quick-actions/{quickActionId}/run", h.RunQuickAction)
			r.Post("/quick-actions/{quickActionId}/render", h.RenderQuickAction)
			r.Get("/task-runs", h.ListTasksByIssue)
			r.Get(routeUsage, h.GetIssueUsage)
			r.Post(routeReactions, h.AddIssueReaction)
			r.Delete(routeReactions, h.RemoveIssueReaction)
			r.Get("/attachments", h.ListAttachments)
			r.Get("/children", h.ListChildIssues)
			r.Get(routeLabels, h.ListLabelsForIssue)
			r.Post(routeLabels, h.AttachLabel)
			r.Delete(routeLabelsWithID, h.DetachLabel)
			r.Get("/metadata", h.ListIssueMetadata)
			r.Put("/metadata/{key}", h.SetIssueMetadataKey)
			r.Delete("/metadata/{key}", h.DeleteIssueMetadataKey)
			r.Put("/properties/{propertyId}", h.SetIssueProperty)
			r.Delete("/properties/{propertyId}", h.DeleteIssueProperty)
			r.Get("/pull-requests", h.ListPullRequestsForIssue)
		})
	})

	// Task messages (user-facing, not daemon auth)
	r.Get("/api/tasks/{taskId}/messages", h.ListTaskMessagesByUser)

	// Issue quick actions (definitions; running one lives under
	// /api/issues/{id}/quick-actions/{quickActionId}/run)
	r.Route("/api/quick-actions", func(r chi.Router) {
		r.Get("/", h.ListQuickActions)
		r.Post("/", h.CreateQuickAction)
		r.Route("/{id}", func(r chi.Router) {
			r.Patch("/", h.UpdateQuickAction)
			r.Delete("/", h.DeleteQuickAction)
		})
	})

	// Custom issue properties (definitions; values live under /api/issues/{id}/properties)
	r.Route("/api/properties", func(r chi.Router) {
		r.Get("/", h.ListProperties)
		r.Post("/", h.CreateProperty)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.GetProperty)
			r.Patch("/", h.UpdateProperty)
		})
	})

	// Labels
	r.Route("/api/labels", func(r chi.Router) {
		r.Get("/", h.ListLabels)
		r.Post("/", h.CreateLabel)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.GetLabel)
			r.Put("/", h.UpdateLabel)
			r.Delete("/", h.DeleteLabel)
		})
	})

	// Issue status catalog (MUL-6243). Reads are open to any member —
	// every client needs the catalog to render a status. Writes are
	// gated to workspace owner/admin inside the handlers.
	r.Route("/api/issue-statuses", func(r chi.Router) {
		r.Get("/", h.ListIssueStatuses)
		r.Post("/", h.CreateIssueStatus)
		r.Patch("/reorder", h.ReorderIssueStatuses)
		r.Route("/{id}", func(r chi.Router) {
			r.Patch("/", h.UpdateIssueStatus)
			r.Delete("/", h.ArchiveIssueStatus)
		})
	})

}

func mountWorkspaceProjectAndSquadRoutes(r chi.Router, h *handler.Handler) {
	// Projects
	r.Route("/api/projects", func(r chi.Router) {
		r.Get(routeSearch, h.SearchProjects)
		r.Get("/", h.ListProjects)
		r.Post("/", h.CreateProject)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.GetProject)
			r.Put("/", h.UpdateProject)
			r.Delete("/", h.DeleteProject)
			r.Get("/resources", h.ListProjectResources)
			r.Post("/resources", h.CreateProjectResource)
			r.Put("/resources/{resourceId}", h.UpdateProjectResource)
			r.Delete("/resources/{resourceId}", h.DeleteProjectResource)
		})
	})

	// Squads
	r.Route("/api/squads", func(r chi.Router) {
		r.Get("/", h.ListSquads)
		r.Post("/", h.CreateSquad)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.GetSquad)
			r.Put("/", h.UpdateSquad)
			r.Delete("/", h.DeleteSquad)
			r.Get(routeMembers, h.ListSquadMembers)
			r.Get("/members/status", h.ListSquadMemberStatus)
			r.Post(routeMembers, h.AddSquadMember)
			r.Delete(routeMembers, h.RemoveSquadMember)
			r.Patch("/members/role", h.UpdateSquadMemberRole)
		})
	})

	// Squad leader evaluation (writes to activity_log)
	r.Post("/api/issues/{id}/squad-evaluated", h.RecordSquadLeaderEvaluation)

	// Autopilots
	r.Route("/api/autopilots", func(r chi.Router) {
		r.Get("/", h.ListAutopilots)
		r.Post("/", h.CreateAutopilot)
		r.Get("/cron-preview", h.CronPreview)
		r.Get(routeUsage, h.GetAutopilotQuotaUsage)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.GetAutopilot)
			r.Patch("/", h.UpdateAutopilot)
			r.Delete("/", h.DeleteAutopilot)
			r.Post("/trigger", h.TriggerAutopilot)
			r.Get("/runs", h.ListAutopilotRuns)
			r.Get("/runs/{runId}", h.GetAutopilotRun)
			r.Get("/deliveries", h.ListAutopilotDeliveries)
			r.Get("/deliveries/{deliveryId}", h.GetAutopilotDelivery)
			r.Post("/deliveries/{deliveryId}/replay", h.ReplayAutopilotDelivery)
			r.Post("/triggers", h.CreateAutopilotTrigger)
			r.Route("/triggers/{triggerId}", func(r chi.Router) {
				r.Patch("/", h.UpdateAutopilotTrigger)
				r.Delete("/", h.DeleteAutopilotTrigger)
				r.Post("/rotate-webhook-token", h.RotateAutopilotTriggerWebhookToken)
				r.Put("/signing-secret", h.SetAutopilotTriggerSigningSecret)
			})
			r.Post("/collaborators", h.AddAutopilotCollaborator)
			r.Delete("/collaborators/{userId}", h.RemoveAutopilotCollaborator)
		})
	})

	// Pins
	r.Route("/api/pins", func(r chi.Router) {
		r.Get("/", h.ListPins)
		r.Post("/", h.CreatePin)
		r.Put("/reorder", h.ReorderPins)
		r.Delete("/{itemType}/{itemId}", h.DeletePin)
	})

	// Saved issue views (MUL-4796).
	r.Get("/api/issue-view-preferences", h.GetIssueViewPreference)
	r.Put("/api/issue-view-preferences", h.PutIssueViewPreference)
	r.Route("/api/issue-views", func(r chi.Router) {
		r.Get("/", h.ListIssueViews)
		r.Post("/", h.CreateIssueView)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.GetIssueViewByID)
			r.Patch("/", h.UpdateIssueView)
			r.Delete("/", h.DeleteIssueView)
		})
	})

	// Attachments
	r.Get("/api/attachments/{id}", h.GetAttachmentByID)
	// /api/attachments/{id}/download is registered in the
	// outer Auth-only group above so it can be loaded as a
	// native <img>/<video> src without workspace headers
	// (MUL-3130). The handler self-resolves the workspace
	// from the attachment row.
	r.Get("/api/attachments/{id}/content", h.GetAttachmentContent)
	r.Delete("/api/attachments/{id}", h.DeleteAttachment)

}

func mountWorkspaceAgentAndChatRoutes(r chi.Router, h *handler.Handler) {
	// Agents
	r.Route("/api/agents", func(r chi.Router) {
		r.Get("/", h.ListAgents)
		r.Post("/", h.CreateAgent)
		// The workspace's built-in Chief of Staff. Server-owned: the
		// caller supplies only a runtime and a language, so a client
		// cannot mint an agent carrying `system_key` and thereby claim
		// the system instruction layer. Idempotent per workspace.
		r.Post("/mika", h.CreateMikaAgent)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.GetAgent)
			r.Put("/", h.UpdateAgent)
			r.Post("/archive", h.ArchiveAgent)
			r.Post("/restore", h.RestoreAgent)
			r.Post("/cancel-tasks", h.CancelAgentTasks)
			r.Get("/tasks", h.ListAgentTasks)
			r.Get("/skills", h.ListAgentSkills)
			r.Put("/skills", h.SetAgentSkills)
			r.Post("/skills/add", h.AddAgentSkills)
			r.Get(routeLabels, h.ListLabelsForAgent)
			r.Post(routeLabels, h.AttachLabelToAgent)
			r.Delete(routeLabelsWithID, h.DetachLabelFromAgent)
			r.Put("/skills/{skillId}/enabled", h.SetAgentSkillEnabled)
			r.Put("/runtime-skills/enabled", h.SetAgentRuntimeSkillEnabled)
			r.Delete("/skills/{skillId}", h.RemoveAgentSkill)
			// Workspace MCP servers assigned to this agent. Mirrors
			// the skills routes above: a library entry does nothing
			// until it is added here, and the binding carries its own
			// enabled toggle.
			r.Get(routeMCPServers, h.ListAgentMcpServers)
			r.Post(routeMCPServers, h.AddAgentMcpServer)
			r.Put("/mcp-servers/{serverId}/enabled", h.SetAgentMcpServerEnabled)
			r.Delete(routeMCPServersWithID, h.RemoveAgentMcpServer)
			// Dedicated env-management endpoint. Admits the agent
			// owner or a workspace owner/admin; agent actors are
			// denied. Every reveal / write is audited to
			// activity_log. See MUL-2600, MUL-5438 and
			// internal/handler/agent_env.go.
			r.Get("/env", h.GetAgentEnv)
			r.Put("/env", h.UpdateAgentEnv)
		})
	})

	r.Route("/api/agent-builder/sessions", func(r chi.Router) {
		// The creation studio's unfinished drafts. Builder sessions are
		// invisible to every chat list (their carrier is kind='system'),
		// so this is the only route back to one.
		r.Get("/", h.ListAgentBuilderSessions)
		r.Post("/", h.CreateAgentBuilderSession)
		r.Patch("/{sessionId}/runtime", h.SwitchAgentBuilderRuntime)
		// Autosaved configuration, including edits the user has typed
		// but not sent. Read back through the list above.
		r.Put("/{sessionId}/draft", h.SaveAgentBuilderDraft)
	})

	// Skills
	r.Route("/api/skills", func(r chi.Router) {
		r.Get("/", h.ListSkills)
		r.Post("/", h.CreateSkill)
		r.Get(routeSearch, h.SearchSkills)
		r.Post("/import", h.ImportSkill)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.GetSkill)
			r.Put("/", h.UpdateSkill)
			r.Delete("/", h.DeleteSkill)
			r.Post("/refresh", h.RefreshSkill)
			r.Get(routeLabels, h.ListLabelsForSkill)
			r.Post(routeLabels, h.AttachLabelToSkill)
			r.Delete(routeLabelsWithID, h.DetachLabelFromSkill)
			r.Get("/files", h.ListSkillFiles)
			r.Put("/files", h.UpsertSkillFile)
			r.Delete("/files/{fileId}", h.DeleteSkillFile)
		})
	})

	// Dashboard — workspace-wide token + run-time rollups for the
	// "/{slug}/dashboard" page. Optional ?project_id filter scopes
	// the rollup to a single project.
	r.Route("/api/dashboard", func(r chi.Router) {
		r.Get("/usage/daily", h.GetDashboardUsageDaily)
		r.Get("/usage/by-agent", h.GetDashboardUsageByAgent)
		r.Get("/agent-runtime", h.GetDashboardAgentRunTime)
		r.Get("/runtime/daily", h.GetDashboardRunTimeDaily)
		r.Get("/failures/daily", h.GetDashboardFailuresDaily)
		r.Get("/failures/by-agent", h.GetDashboardFailuresByAgent)
	})

	// Runtimes
	r.Route("/api/runtimes", func(r chi.Router) {
		r.Get("/", h.ListAgentRuntimes)
		r.Route("/{runtimeId}", func(r chi.Router) {
			r.Patch("/", h.UpdateAgentRuntime)
			r.Get(routeUsage, h.GetRuntimeUsage)
			r.Get("/usage/by-agent", h.GetRuntimeUsageByAgent)
			r.Get("/usage/by-hour", h.GetRuntimeUsageByHour)
			r.Get("/activity", h.GetRuntimeTaskActivity)
			r.Post("/update", h.InitiateUpdate)
			r.Get("/update/{updateId}", h.GetUpdate)
			r.Post("/models", h.InitiateListModels)
			r.Get("/models/{requestId}", h.GetModelListRequest)
			r.Post("/local-skills", h.InitiateListLocalSkills)
			r.Get("/local-skills/{requestId}", h.GetLocalSkillListRequest)
			r.Post("/local-skills/import", h.InitiateImportLocalSkill)
			r.Get("/local-skills/import/{requestId}", h.GetLocalSkillImportRequest)
			r.Delete("/", h.DeleteAgentRuntime)
			// Confirmed variant of DELETE: unbind every agent bound to
			// this runtime (they keep their configuration and chats and
			// need a new runtime to run again), cancel their tasks,
			// detach their task history, then delete the runtime — all
			// in one transaction. Used by the DeleteRuntimeDialog when
			// the strict DELETE refused with
			// `runtime_has_active_agents` and the user confirmed.
			r.Post("/unbind-agents-and-delete", h.UnbindAgentsAndDeleteRuntime)
			// Legacy path for installed clients built against the
			// archive-and-delete contract (MUL-5559 renamed the
			// behaviour, not just the route). Same handler.
			r.Post("/archive-agents-and-delete", h.UnbindAgentsAndDeleteRuntime)
		})
	})

	// Cloud Runtime fleet proxy. The remote service URL is configured
	// on SaaS API nodes only; self-hosted deployments return 503.
	r.Route("/api/cloud-runtime", func(r chi.Router) {
		r.Get("/", h.GetCloudRuntimeService)
		r.Get("/healthz", h.GetCloudRuntimeHealth)
		r.Get("/readyz", h.GetCloudRuntimeReady)
		r.Get(routeNodes, h.ListCloudRuntimeNodes)
		r.Post(routeNodes, h.CreateCloudRuntimeNode)
		r.Delete(routeNodes, h.DeleteCloudRuntimeNode)
		r.Post("/nodes/start", h.StartCloudRuntimeNode)
		r.Post("/nodes/stop", h.StopCloudRuntimeNode)
		r.Post("/nodes/reboot", h.RebootCloudRuntimeNode)
		r.Post("/nodes/status", h.GetCloudRuntimeNodeStatus)
		r.Post("/nodes/exec", h.ExecCloudRuntimeNode)
	})

	// Tasks (user-facing, with ownership check)
	r.Post("/api/tasks/{taskId}/cancel", h.CancelTaskByUser)

	// Workspace-wide agent task snapshot for presence derivation:
	// every active task + each agent's most recent terminal task.
	r.Get("/api/agent-task-snapshot", h.ListWorkspaceAgentTaskSnapshot)

	// Independent workspace-level list backing the issues-header
	// "agents working" chip and its assignee-id Table filter.
	r.Get("/api/working-agents", h.ListWorkspaceWorkingAgents)

	// Workspace-wide daily agent activity (last 30d, anchored on
	// completed_at). Backs the Agents-list sparkline (trailing 7d
	// slice) AND the agent detail "Last 30 days" panel.
	r.Get("/api/agent-activity-30d", h.GetWorkspaceAgentActivity30d)

	// Workspace-wide 30-day run counts per agent for the Agents-list RUNS column.
	r.Get("/api/agent-run-counts", h.GetWorkspaceAgentRunCounts)

	r.Route("/api/chat/sessions", func(r chi.Router) {
		r.Post("/", h.CreateChatSession)
		r.Get("/", h.ListChatSessions)
		r.Route("/{sessionId}", func(r chi.Router) {
			r.Get("/", h.GetChatSession)
			r.Patch("/", h.UpdateChatSession)
			r.Patch("/pin", h.SetChatSessionPinned)
			r.Patch("/archive", h.SetChatSessionArchived)
			r.Delete("/", h.DeleteChatSession)
			r.Post("/messages", h.SendChatMessage)
			r.Post("/onboarding", h.StartMikaOnboarding)
			// Explicit "refresh" of a turn's quick actions: re-runs the
			// daemon suggestion pass for the latest assistant reply (MUL-5149).
			r.Post("/quick-actions/regenerate", h.RegenerateChatQuickActions)
			r.Get("/messages", h.ListChatMessages)
			r.Get("/messages/page", h.ListChatMessagesPage)
			r.Get("/pending-task", h.GetPendingChatTask)
			r.Delete("/queued-tasks", h.ClearQueuedChatTasks)
			r.Post("/queued-tasks/{taskId}/prioritize", h.PrioritizeQueuedChatTask)
			r.Post("/read", h.MarkChatSessionRead)
			// Deferred-cancellation draft restores (#5219):
			// creator-only fetch + idempotent consume.
			r.Get("/draft-restores", h.ListChatDraftRestores)
			r.Delete("/draft-restores/{restoreId}", h.ConsumeChatDraftRestore)
		})
	})
	r.Get("/api/chat/pending-tasks", h.ListPendingChatTasks)
	r.Get("/api/chat/pending-tasks/has-any", h.HasPendingChatTasks)

	// Quick-agent bar: per-user pinned agents for one-tap new chats.
	r.Get("/api/chat/pinned-agents", h.ListChatPinnedAgents)
	r.Post("/api/chat/pinned-agents", h.PinChatAgent)
	r.Delete("/api/chat/pinned-agents/{agentId}", h.UnpinChatAgent)

	// Agent-facing channel reads (MUL-3871). The caller's task-scoped token
	// resolves to its own chat session; no session/channel id is passed, so
	// an agent can only read its own conversation. `history` is the channel
	// overview (top-level messages + thread metadata); `thread` reads one
	// thread (?id for a specific one, else the thread the session is in).
	r.Get("/api/chat/history", h.GetChatChannelHistory)
	r.Get("/api/chat/thread", h.GetChatThread)

}

func mountWorkspaceInboxAndCommentRoutes(r chi.Router, h *handler.Handler) {
	// Comments
	r.Route("/api/comments/{commentId}", func(r chi.Router) {
		r.Put("/", h.UpdateComment)
		r.Delete("/", h.DeleteComment)
		r.Post("/resolve", h.ResolveComment)
		r.Delete("/resolve", h.UnresolveComment)
		r.Post(routeReactions, h.AddReaction)
		r.Delete(routeReactions, h.RemoveReaction)
	})

	// Inbox
	r.Route("/api/inbox", func(r chi.Router) {
		r.Get("/", h.ListInbox)
		// Archived notifications, for the inbox's "Archived" sub-view.
		// Separate from "/" so the main list keeps its contract and
		// never carries the unbounded archive.
		r.Get("/archived", h.ListArchivedInbox)
		r.Get("/unread-count", h.CountUnreadInbox)
		// Cross-workspace unread summary: account-level, keyed on the
		// user. Backs the workspace-switcher dot for OTHER workspaces.
		r.Get("/unread-summary", h.UnreadInboxSummary)
		r.Post("/mark-all-read", h.MarkAllInboxRead)
		r.Post("/archive-all", h.ArchiveAllInbox)
		r.Post("/archive-all-read", h.ArchiveAllReadInbox)
		r.Post("/archive-completed", h.ArchiveCompletedInbox)
		r.Post("/{id}/read", h.MarkInboxRead)
		r.Post("/{id}/unread", h.MarkInboxUnread)
		r.Post("/{id}/archive", h.ArchiveInboxItem)
		r.Post("/{id}/unarchive", h.UnarchiveInboxItem)
	})

	// Notification preferences
	r.Route("/api/notification-preferences", func(r chi.Router) {
		r.Get("/", h.GetNotificationPreferences)
		r.Patch("/", h.PatchNotificationPreferences)
		r.Put("/", h.UpdateNotificationPreferences)
	})
}
