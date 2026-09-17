package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/cloudruntime"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/entitlement"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/integrations/channel"
	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
	"github.com/multica-ai/multica/server/internal/integrations/lark"
	"github.com/multica-ai/multica/server/internal/integrations/wecom"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/sso"
	"github.com/multica-ai/multica/server/internal/storage"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/featureflag"
	"github.com/multica-ai/multica/server/pkg/llm"
)

var defaultOrigins = []string{
	"http://localhost:3000", // Next.js dev
	"http://localhost:5173", // electron-vite dev
	"http://localhost:5174", // electron-vite dev (fallback port)
}

// corsAllowedHeaders must list every header the browser clients send. A header
// missing here fails the preflight, so the request never reaches the handler at
// all — the failure looks nothing like "the server ignored my header".
// X-Client-Capabilities in particular was daemon-only (a Go client, never
// preflighted) until the web app started advertising chat-draft-restore-v1 on
// cancel.
var corsAllowedHeaders = []string{
	"Accept",
	"Authorization",
	"Content-Type",
	"Idempotency-Key",
	"X-Workspace-ID",
	"X-Workspace-Slug",
	"X-Request-ID",
	"X-Agent-ID",
	"X-Task-ID",
	"X-CSRF-Token",
	"X-Client-Platform",
	"X-Client-Version",
	"X-Client-OS",
	"X-Client-Capabilities",
	// Sent by the host page when it relays a plugin surface's Action API call.
	"X-Multica-Plugin-Installation",
}

// corsExposedHeaders lists response headers browser clients are allowed to read.
// Without this a custom response header is silently unreadable from JS on a
// cross-origin request (only the CORS-safelisted response headers are exposed by
// default) — the header arrives on the wire and then disappears, which looks
// exactly like the server never sent it.
//
// Referencing the handler constant rather than re-typing the string keeps a
// rename from quietly switching the signal off (MUL-5492).
var corsExposedHeaders = []string{
	handler.HeaderCommentsTruncated,
	handler.HeaderTimelineTruncated,
}

func allowedOrigins() []string {
	raw := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("FRONTEND_ORIGIN"))
	}
	if raw == "" {
		return defaultOrigins
	}

	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		origin := strings.TrimSpace(part)
		if origin != "" {
			origins = append(origins, origin)
		}
	}
	if len(origins) == 0 {
		return defaultOrigins
	}
	return origins
}

// appURLFromEnv resolves the user-facing web app URL. It prefers
// MULTICA_APP_URL and falls back to FRONTEND_ORIGIN, matching how the backend
// resolves the app URL elsewhere (handler.daemonSetupURLsFromEnv) and the CLI
// login flow (cmd/multica tryResolveAppURL). Empty when neither is set.
func appURLFromEnv() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("MULTICA_APP_URL")), "/"); v != "" {
		return v
	}
	return strings.TrimRight(strings.TrimSpace(os.Getenv("FRONTEND_ORIGIN")), "/")
}

// parseTrustedProxies parses a comma-separated list of CIDR prefixes from the
// MULTICA_TRUSTED_PROXIES env var. Invalid entries are dropped with a single
// warn-line per entry rather than crashing the server — a typo in one CIDR
// shouldn't take the whole API down. Returns nil for empty input, which the
// rate limiter treats as "trust no proxy headers, use RemoteAddr only".
func parseTrustedProxies(raw string) []netip.Prefix {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []netip.Prefix
	for _, part := range strings.Split(raw, ",") {
		s := strings.TrimSpace(part)
		if s == "" {
			continue
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			slog.Warn("MULTICA_TRUSTED_PROXIES: ignoring invalid CIDR",
				"value", s, "error", err)
			continue
		}
		out = append(out, p)
	}
	return out
}

// normalizeServerVersion maps the unstamped "dev" default (main.go's
// `version` var, unchanged when the binary wasn't built with
// -X main.version=<tag>) to an empty string. handler.Config.ServerVersion
// feeds /api/config's server_version field with omitempty, so an empty
// string hides the Help popover's version row instead of rendering
// "Server version dev" for a local `go build`/`go run` or a self-hosted
// `docker build` without --build-arg VERSION.
func normalizeServerVersion(v string) string {
	if v == "dev" {
		return ""
	}
	return v
}

// NewRouter creates the fully-configured Chi router with all middleware and routes.
// rdb is optional: when non-nil the runtime local-skill request stores are
// swapped for Redis-backed implementations so multiple API nodes share the
// same pending queue (required for multi-node prod). This should be a request
// path Redis client, not the realtime relay's blocking read client. A nil rdb
// keeps the default in-memory stores which are fine for single-node dev and
// tests.
func NewRouter(pool *pgxpool.Pool, hub *realtime.Hub, bus *events.Bus, analyticsClient analytics.Client, rdb *redis.Client) chi.Router {
	r, _ := NewRouterWithOptions(pool, hub, bus, analyticsClient, rdb, RouterOptions{})
	return r
}

type RouterOptions struct {
	HTTPMetrics         *obsmetrics.HTTPMetrics
	BusinessMetrics     *obsmetrics.BusinessMetrics
	ChannelLeaseMetrics *obsmetrics.ChannelLeaseMetrics
	// ChannelLeaseRedis is a dedicated non-blocking Redis client/pool. It is
	// required only when CHANNEL_WS_LEASE_BACKEND=redis.
	ChannelLeaseRedis *redis.Client
	// WecomMetrics is the WeCom adapter's health sink. Nil discards every
	// counter, which is what a deployment with /metrics turned off gets.
	WecomMetrics *obsmetrics.WecomMetrics
	DaemonHub    *daemonws.Hub
	DaemonWakeup service.TaskWakeupNotifier
	FeatureFlags *featureflag.Service
	// HeartbeatScheduler, when non-nil, replaces the default synchronous
	// passthrough scheduler on the constructed Handler. main.go injects a
	// BatchedHeartbeatScheduler here so the caller can also drive Run/Stop;
	// tests leave this nil and get the legacy synchronous behavior.
	HeartbeatScheduler handler.HeartbeatScheduler
	// LLMMaxRetries carries the parsed MULTICA_LLM_MAX_RETRIES budget. Unlike
	// its three MULTICA_LLM_* siblings it is injected rather than read here,
	// because an invalid value must fail the boot and only main() can exit —
	// terminating the process from inside a router constructor would also kill
	// any test that happened to have the variable set. nil means unset, which
	// is what tests and NewRouter get.
	LLMMaxRetries *llm.RetryOverride
}

func buildChannelSupervisor(
	installations engine.InstallationStore,
	postgresLeases engine.LeaseStore,
	registry *channel.Registry,
	inbound channel.InboundHandler,
	opts RouterOptions,
) *engine.Supervisor {
	cfg, err := channelSupervisorConfigFromEnv(opts.ChannelLeaseMetrics)
	if err != nil {
		slog.Error("channel engine: invalid lease configuration; supervisor disabled", "error", err)
		return nil
	}

	backend := strings.ToLower(strings.TrimSpace(os.Getenv("CHANNEL_WS_LEASE_BACKEND")))
	if backend == "" {
		backend = "postgres"
	}
	var leases engine.LeaseStore
	switch backend {
	case "postgres":
		leases = postgresLeases
	case "redis":
		if opts.ChannelLeaseRedis == nil {
			slog.Error("channel engine: Redis lease backend selected but CHANNEL_WS_LEASE_REDIS_URL/REDIS_URL is missing or invalid; supervisor disabled")
			return nil
		}
		namespace := strings.TrimSpace(os.Getenv("CHANNEL_WS_LEASE_NAMESPACE"))
		redisLeases, err := engine.NewRedisLeaseStore(opts.ChannelLeaseRedis, namespace)
		if err != nil {
			slog.Error("channel engine: Redis lease configuration invalid; supervisor disabled", "error", err)
			return nil
		}
		readyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = redisLeases.Ready(readyCtx)
		cancel()
		if err != nil {
			slog.Error("channel engine: Redis lease backend unavailable; supervisor disabled", "error", err)
			return nil
		}
		leases = redisLeases
	default:
		slog.Error("channel engine: unsupported CHANNEL_WS_LEASE_BACKEND; supervisor disabled", "backend", backend)
		return nil
	}

	slog.Info("channel engine: lease backend configured",
		"backend", backend,
		"ttl", cfg.LeaseTTL.String(),
		"renew_interval", cfg.LeaseRenewInterval.String(),
		"poll_interval", cfg.PollInterval.String(),
	)
	return engine.NewSupervisor(installations, leases, registry, inbound, cfg)
}

func channelSupervisorConfigFromEnv(leaseMetrics *obsmetrics.ChannelLeaseMetrics) (engine.Config, error) {
	ttl, err := strictPositiveDurationEnv("CHANNEL_WS_LEASE_TTL", 180*time.Second)
	if err != nil {
		return engine.Config{}, err
	}
	renew, err := strictPositiveDurationEnv("CHANNEL_WS_LEASE_RENEW_INTERVAL", 60*time.Second)
	if err != nil {
		return engine.Config{}, err
	}
	poll, err := strictPositiveDurationEnv("CHANNEL_WS_LEASE_POLL_INTERVAL", 30*time.Second)
	if err != nil {
		return engine.Config{}, err
	}
	retry, err := strictPositiveDurationEnv("CHANNEL_WS_LEASE_ERROR_RETRY_INTERVAL", 5*time.Second)
	if err != nil {
		return engine.Config{}, err
	}
	margin, err := strictPositiveDurationEnv("CHANNEL_WS_LEASE_EXPIRY_SAFETY_MARGIN", 5*time.Second)
	if err != nil {
		return engine.Config{}, err
	}
	cfg := engine.Config{
		LeaseTTL:                ttl,
		LeaseRenewInterval:      renew,
		PollInterval:            poll,
		LeaseErrorRetryInterval: retry,
		LeaseExpirySafetyMargin: margin,
		LeaseMetrics:            leaseMetrics,
		Logger:                  slog.Default(),
	}
	if err := cfg.Validate(); err != nil {
		return engine.Config{}, err
	}
	return cfg, nil
}

func strictPositiveDurationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration (got %q)", name, raw)
	}
	return value, nil
}

// NewRouterWithOptions builds the fully-configured Chi router and
// returns the *handler.Handler it was constructed from. Callers that
// need to drive background lifecycle on services attached to the
// handler (e.g. starting the Lark inbound Hub under a long-running
// context, calling Wait on shutdown) use the returned handler;
// callers that only need the HTTP handler (tests, the simple
// NewRouter shim) discard the second value.
func NewRouterWithOptions(pool *pgxpool.Pool, hub *realtime.Hub, bus *events.Bus, analyticsClient analytics.Client, rdb *redis.Client, opts RouterOptions) (chi.Router, *handler.Handler) {
	queries := db.New(pool)
	emailSvc := service.NewEmailService()
	daemonHub := opts.DaemonHub
	if daemonHub == nil {
		daemonHub = daemonws.NewHub()
	}

	// Initialize storage with S3 as primary, fallback to local
	var store storage.Storage
	s3 := storage.NewS3StorageFromEnv()
	if s3 != nil {
		store = s3
	} else {
		local := storage.NewLocalStorageFromEnv()
		if local != nil {
			store = local
		}
	}

	cfSigner := auth.NewCloudFrontSignerFromEnv()
	origins := allowedOrigins()

	signupConfig := handler.Config{
		AllowSignup:              os.Getenv("ALLOW_SIGNUP") != "false",
		AllowedEmails:            splitAndTrim(os.Getenv("ALLOWED_EMAILS")),
		AllowedEmailDomains:      splitAndTrim(os.Getenv("ALLOWED_EMAIL_DOMAINS")),
		DisableWorkspaceCreation: os.Getenv("DISABLE_WORKSPACE_CREATION") == "true",
		VCSIntegrationEnabled:    os.Getenv("MULTICA_VCS_INTEGRATION_ENABLED") == "true",
		PublicURL:                strings.TrimRight(strings.TrimSpace(os.Getenv("MULTICA_PUBLIC_URL")), "/"),
		TrustedProxies:           parseTrustedProxies(os.Getenv("MULTICA_TRUSTED_PROXIES")),
		CloudRuntimeFleetURL:     cloudRuntimeFleetURLFromEnv(),
		CloudRuntimeFleetTimeout: envDuration("MULTICA_CLOUD_FLEET_TIMEOUT", 35*time.Second),
		AttachmentDownloadMode:   os.Getenv("ATTACHMENT_DOWNLOAD_MODE"),
		AttachmentDownloadURLTTL: envDuration("ATTACHMENT_DOWNLOAD_URL_TTL", 30*time.Minute),
		AttachmentFrameAncestors: origins,
		LLMAPIKey:                strings.TrimSpace(os.Getenv("MULTICA_LLM_API_KEY")),
		LLMBaseURL:               strings.TrimSpace(os.Getenv("MULTICA_LLM_BASE_URL")),
		LLMDefaultModel:          strings.TrimSpace(os.Getenv("MULTICA_LLM_DEFAULT_MODEL")),
		SSOEnabled:               os.Getenv("MULTICA_SSO_ENABLED") == "true",
		SSOIssuer:                strings.TrimSpace(os.Getenv("MULTICA_SSO_KEYCLOAK_ISSUER")),
		SSOClientID:              strings.TrimSpace(os.Getenv("MULTICA_SSO_CLIENT_ID")),
		SSOClientSecret:          strings.TrimSpace(os.Getenv("MULTICA_SSO_CLIENT_SECRET")),
		SSORedirectURL:           strings.TrimSpace(os.Getenv("MULTICA_SSO_REDIRECT_URL")),
		SSOSkipTLSVerify:         os.Getenv("MULTICA_SSO_SKIP_TLS_VERIFY") == "true",
		LLMMaxRetries:            opts.LLMMaxRetries,
		ServerVersion:            normalizeServerVersion(version),
	}
	h := handler.New(queries, pool, hub, bus, emailSvc, store, cfSigner, analyticsClient, signupConfig, daemonHub)
	invitationRateLimits := handler.DefaultInvitationRateLimits()
	invitationRateLimits.Actor.Limit = envNonNegativeInt("RATE_LIMIT_INVITATION_ACTOR_10M", invitationRateLimits.Actor.Limit)
	invitationRateLimits.Workspace.Limit = envNonNegativeInt("RATE_LIMIT_INVITATION_WORKSPACE_24H", invitationRateLimits.Workspace.Limit)
	invitationRateLimits.Recipient.Limit = envNonNegativeInt("RATE_LIMIT_INVITATION_RECIPIENT_24H", invitationRateLimits.Recipient.Limit)
	h.InvitationRateLimiters = handler.NewMemoryInvitationRateLimiters(invitationRateLimits)
	// SSO / Keycloak setup (optional, env-gated). Discovery happens at startup;
	// failure is fatal because a misconfigured issuer would break every login.
	if signupConfig.SSOEnabled {
		oidcClient, err := sso.NewOIDCClient(context.Background(),
			signupConfig.SSOIssuer,
			signupConfig.SSOClientID,
			signupConfig.SSOClientSecret,
			signupConfig.SSORedirectURL,
			signupConfig.SSOSkipTLSVerify,
		)
		if err != nil {
			slog.Error("sso: keycloak oidc init failed", "error", err)
			os.Exit(1)
		}
		h.OIDC = oidcClient
		slog.Info("sso: keycloak oidc enabled", "issuer", signupConfig.SSOIssuer)
	}
	h.Metrics = opts.BusinessMetrics
	h.FeatureFlags = opts.FeatureFlags
	h.TaskService.FeatureFlags = opts.FeatureFlags
	h.TaskService.Metrics = opts.BusinessMetrics
	h.IssueService.Metrics = opts.BusinessMetrics
	entitlementClient, entitlementErr := entitlement.New(entitlement.Config{
		Enabled:      envBool("MULTICA_ENTITLEMENT_POLICY_ENABLED", false),
		BaseURL:      strings.TrimSpace(os.Getenv("MULTICA_ENTITLEMENT_POLICY_URL")),
		ServiceToken: os.Getenv("MULTICA_ENTITLEMENT_SERVICE_TOKEN"),
		Timeout:      envDuration("MULTICA_ENTITLEMENT_POLICY_TIMEOUT", 3*time.Second),
		StaleGrace:   envNonNegativeDuration("MULTICA_ENTITLEMENT_STALE_GRACE", 15*time.Minute),
		Observer:     opts.BusinessMetrics,
	})
	if entitlementErr != nil {
		slog.Error("entitlement policy client disabled by invalid configuration", "error", entitlementErr)
		opts.BusinessMetrics.RecordEntitlementConfigError()
	} else if entitlementClient.Enabled() {
		entitlementClient.SetEmergencyDisabled(envBool("MULTICA_ENTITLEMENT_EMERGENCY_DISABLED", false))
		h.AutopilotService.Entitlements = entitlementClient
		h.AutopilotService.QuotaMetrics = opts.BusinessMetrics
	}
	if opts.BusinessMetrics != nil {
		// Wire the BusinessMetrics receiver into the cloud runtime client
		// so every outbound Fleet/Gateway request feeds the
		// multica_cloudruntime_request_* histograms.
		if client, ok := h.CloudRuntime.(*cloudruntime.Client); ok {
			client.SetRecorder(opts.BusinessMetrics)
		}
	}
	if opts.DaemonWakeup != nil {
		h.TaskService.Wakeup = opts.DaemonWakeup
		if notifier, ok := opts.DaemonWakeup.(handler.RuntimeProfileRefreshNotifier); ok {
			h.DaemonProfileRefresh = notifier
		}
		if notifier, ok := opts.DaemonWakeup.(handler.WorkspaceSetRefreshNotifier); ok {
			h.DaemonWorkspaceRefresh = notifier
		}
		if notifier, ok := opts.DaemonWakeup.(handler.DaemonPendingWorkNotifier); ok {
			h.DaemonPendingWork = notifier
		}
	}
	if rdb != nil {
		h.UpdateStore = handler.NewRedisUpdateStore(rdb)
		h.ModelListStore = handler.NewRedisModelListStore(rdb)
		h.ModelCatalogCache = handler.NewRedisModelCatalogCache(rdb)
		h.LocalSkillListStore = handler.NewRedisLocalSkillListStore(rdb)
		h.LocalSkillImportStore = handler.NewRedisLocalSkillImportStore(rdb)
		h.LivenessStore = handler.NewRedisLivenessStore(rdb)
		h.WebhookRateLimiter = handler.NewRedisWebhookRateLimiter(rdb, handler.DefaultWebhookRateLimit())
		h.WebhookIPRateLimiter = handler.NewRedisWebhookIPRateLimiter(rdb, handler.DefaultWebhookIPRateLimit())
		h.WebhookAbsoluteIPRateLimiter = handler.NewRedisWebhookAbsoluteIPRateLimiter(rdb, handler.DefaultWebhookAbsoluteIPRateLimit())
		h.InvitationRateLimiters = handler.NewRedisInvitationRateLimiters(rdb, invitationRateLimits)
	}

	patCache, daemonTokenCache, cloudPATVerifier := initServerIntegrations(h, queries, pool, bus, store, rdb, daemonHub, opts, signupConfig)

	health := newServerHealth(pool)

	r := chi.NewRouter()

	// Global middleware
	r.Use(chimw.RequestID)
	r.Use(middleware.ClientMetadata)
	r.Use(middleware.RequestLogger)
	if opts.HTTPMetrics != nil {
		r.Use(opts.HTTPMetrics.Middleware)
	}
	r.Use(chimw.Recoverer)
	r.Use(middleware.ContentSecurityPolicy)

	// Share allowed origins with WebSocket origin checker.
	realtime.SetAllowedOrigins(origins)

	// Share the same trusted-proxy CIDRs (MULTICA_TRUSTED_PROXIES) so the
	// WebSocket origin check honors X-Forwarded-Host only from trusted proxies,
	// using one config source instead of a parallel one.
	realtime.SetTrustedProxies(signupConfig.TrustedProxies)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   corsAllowedHeaders,
		ExposedHeaders:   corsExposedHeaders,
		AllowCredentials: true,
		MaxAge:           300,
	}))

	mountAllRoutes(r, h, health, store, hub, queries, patCache, daemonTokenCache, cloudPATVerifier, rdb)

	return r, h
}

// buildLarkConnector wires the real WS long-conn connector that talks
// to /callback/ws/endpoint directly with app_id/app_secret. The
// connector wraps every read with a ctx-cancel watchdog so lease loss /
// shutdown breaks the blocking ReadMessage in bounded time — the
// invariant §4.4 leans on. A single connector instance serves every
// installation; its Run is parameterized by the installation, so the
// feishuChannel hands it the per-installation row.
//
// If the endpoint fetcher fails to initialize (typically a malformed
// MULTICA_LARK_CALLBACK_BASE_URL), we log and fall back to the
// NoopConnector so the lease / supervisor lifecycle still exercises
// against real DB rows. Inbound messages are silently dropped until
// the config is fixed; the boot log labels the mode "noop" so the
// degraded state is visible.
//
// Returns the connector plus a short label for the boot log:
// "ws-long-conn" in the healthy case, "noop" in the fallback case.
func buildLarkConnector(installSvc *lark.InstallationService, apiClient lark.APIClient) (lark.EventConnector, string) {
	endpointFetcher, err := lark.NewHTTPConnectionTokenFetcher(lark.HTTPConnectionTokenConfig{
		BaseURL: strings.TrimSpace(os.Getenv("MULTICA_LARK_CALLBACK_BASE_URL")),
		Logger:  slog.Default(),
	})
	if err != nil {
		slog.Error("lark ws: endpoint fetcher init failed; falling back to noop", "error", err)
		return lark.NewNoopConnector(slog.Default()), "noop"
	}
	decoder := lark.NewLarkJSONFrameDecoder()
	dialer := lark.NewGorillaDialer()
	if proxyURL := strings.TrimSpace(os.Getenv("MULTICA_LARK_WS_PROXY_URL")); proxyURL != "" {
		dialer.ProxyURL = proxyURL
	}
	credsProvider := lark.CredentialsProviderFunc(func(ctx context.Context, inst lark.Installation) (lark.InstallationCredentials, error) {
		secret, err := installSvc.DecryptAppSecret(inst)
		if err != nil {
			return lark.InstallationCredentials{}, err
		}
		creds := lark.InstallationCredentials{
			AppID:     inst.AppID,
			AppSecret: secret,
			Region:    lark.RegionOrDefault(inst.Region),
		}
		if inst.TenantKey.Valid {
			creds.TenantKey = inst.TenantKey.String
		}
		return creds, nil
	})
	// Inbound enricher: expands quoted replies / forwarded bundles AND
	// prefetches a window of surrounding group history (MUL-3084) into the
	// agent's body via the IM API before dispatch. It shares the
	// connector's resolved credentials and runs under the connector's
	// EnrichTimeout so it cannot overrun the Lark long-conn ACK budget.
	enricher := lark.NewInboundEnricher(apiClient, lark.InboundEnricherConfig{
		RecentContextSize: lark.DefaultRecentContextSize,
		Logger:            slog.Default(),
	})
	conn, err := lark.NewWSLongConnConnector(lark.WSConnectorConfig{
		Dialer:              dialer,
		EndpointFetcher:     endpointFetcher,
		FrameDecoder:        decoder,
		Enricher:            enricher,
		CredentialsProvider: credsProvider,
		Logger:              slog.Default(),
	})
	if err != nil {
		slog.Error("lark ws: connector init failed; falling back to noop", "error", err)
		return lark.NewNoopConnector(slog.Default()), "noop"
	}
	return conn, "ws-long-conn"
}

// membershipChecker implements realtime.MembershipChecker using database queries.
type membershipChecker struct {
	queries *db.Queries
}

func (mc *membershipChecker) IsMember(ctx context.Context, userID, workspaceID string) bool {
	_, err := mc.queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      parseUUID(userID),
		WorkspaceID: parseUUID(workspaceID),
	})
	return err == nil
}

// patResolver implements realtime.PATResolver using database queries.
// patCache is shared with the Auth and DaemonAuth middlewares so a token
// revoke through any path invalidates the cache for all of them. Nil
// cache is supported and degrades to direct DB lookups.
type patResolver struct {
	queries *db.Queries
	cache   *auth.PATCache
}

func (pr *patResolver) ResolveToken(ctx context.Context, token string) (string, bool) {
	hash := auth.HashToken(token)

	if userID, ok := pr.cache.Get(ctx, hash); ok {
		return userID, true
	}

	pat, err := pr.queries.GetPersonalAccessTokenByHash(ctx, hash)
	if err != nil {
		return "", false
	}

	userID := util.UUIDToString(pat.UserID)

	var expiresAt time.Time
	if pat.ExpiresAt.Valid {
		expiresAt = pat.ExpiresAt.Time
	}
	pr.cache.Set(ctx, hash, userID, auth.TTLForExpiry(time.Now(), expiresAt))

	// Cache miss = first WS auth in this TTL window. Refresh last_used_at;
	// subsequent connects within the window skip the write.
	go pr.queries.UpdatePersonalAccessTokenLastUsed(context.Background(), pat.ID)

	return userID, true
}

// parseUUID is a thin alias for util.MustParseUUID. Call sites here are all
// internal round-trips of DB-sourced UUIDs (e.g. issue.ID, e.ActorID), so an
// invalid value indicates a programming error and should panic loudly.
func parseUUID(s string) pgtype.UUID {
	return util.MustParseUUID(s)
}

// optionalUUID returns a NULL pgtype.UUID for an empty string and otherwise
// behaves like parseUUID. Use this for actor IDs on events where the producer
// may legitimately be a "system" actor with no member/agent attribution
// (e.g. GitHub webhook auto-status sync) — the activity_log and inbox_item
// tables both allow actor_id to be NULL.
func optionalUUID(s string) pgtype.UUID {
	if s == "" {
		return pgtype.UUID{}
	}
	return util.MustParseUUID(s)
}

func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	res := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			res = append(res, trimmed)
		}
	}
	return res
}

func cloudRuntimeFleetURLFromEnv() string {
	if url := strings.TrimSpace(os.Getenv("MULTICA_CLOUD_FLEET_URL")); url != "" {
		return url
	}
	return strings.TrimSpace(os.Getenv("MULTICA_FLEET_URL"))
}

// composioStateSecret resolves the HMAC key for the connect-state. Prefers an
// explicit COMPOSIO_STATE_SECRET; otherwise derives a composio-specific key
// from JWT_SECRET via SHA-256 so the two signing domains never share an
// identical key. Returns nil when neither is set (composio stays disabled).
func composioStateSecret() []byte {
	if v := strings.TrimSpace(os.Getenv("COMPOSIO_STATE_SECRET")); v != "" {
		return []byte(v)
	}
	if v := strings.TrimSpace(os.Getenv("JWT_SECRET")); v != "" {
		sum := sha256.Sum256([]byte("composio-state:" + v))
		return sum[:]
	}
	return nil
}

// composioCallbackBaseURL resolves the public API base used to build the
// Composio callback URL. Prefers COMPOSIO_CALLBACK_BASE_URL, then the
// already-resolved MULTICA_PUBLIC_URL, then the app URL.
func composioCallbackBaseURL(publicURL string) string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("COMPOSIO_CALLBACK_BASE_URL")), "/"); v != "" {
		return v
	}
	if publicURL != "" {
		return publicURL
	}
	return appURLFromEnv()
}

// wecomMetricsOrNil keeps a typed nil out of the adapter's interface field.
// A *WecomMetrics that is nil still satisfies wecom.Metrics, so assigning it
// directly would give the adapter a non-nil interface holding a nil pointer —
// and the first counter call would panic on a deployment with /metrics off.
func wecomMetricsOrNil(m *obsmetrics.WecomMetrics) wecom.Metrics {
	if m == nil {
		return nil
	}
	return m
}
