package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/logger"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/profiling"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/scheduler"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/featureflag"
	"github.com/multica-ai/multica/server/pkg/llm"
	"github.com/redis/go-redis/v9"
)

var (
	version = "dev"
	commit  = "unknown"
)

func newNamedRedisClient(base *redis.Options, suffix string) *redis.Client {
	opts := *base
	if envBool("REDIS_DISABLE_CLIENT_NAME", false) {
		opts.ClientName = ""
	} else {
		opts.ClientName = redisClientName(opts.ClientName, suffix)
	}
	return redis.NewClient(&opts)
}

func redisClientName(existing, suffix string) string {
	if suffix == "" {
		return existing
	}
	if existing != "" {
		return existing + ":" + suffix
	}
	return "multica-api:" + suffix
}

func channelLeaseRedisURLFromEnv() string {
	if dedicated := strings.TrimSpace(os.Getenv("CHANNEL_WS_LEASE_REDIS_URL")); dedicated != "" {
		return dedicated
	}
	return strings.TrimSpace(os.Getenv("REDIS_URL"))
}

func realtimeRelayRedisURLFromEnv() string {
	if dedicated := strings.TrimSpace(os.Getenv("REALTIME_RELAY_REDIS_URL")); dedicated != "" {
		return dedicated
	}
	return strings.TrimSpace(os.Getenv("REDIS_URL"))
}

func closeRedisClient(label string, client *redis.Client) {
	if client == nil {
		return
	}
	if err := client.Close(); err != nil {
		slog.Warn("redis client close failed", "client", label, "error", err)
	}
}

func shardedRelayConfigFromEnv() realtime.ShardedStreamRelayConfig {
	cfg := realtime.DefaultShardedStreamRelayConfig()
	cfg.Shards = envPositiveInt("REALTIME_RELAY_SHARDS", cfg.Shards)
	cfg.StreamMaxLen = envPositiveInt64("REALTIME_RELAY_STREAM_MAXLEN", cfg.StreamMaxLen)
	cfg.ReadCount = envPositiveInt64("REALTIME_RELAY_XREAD_COUNT", cfg.ReadCount)
	cfg.ReadBlock = envDuration("REALTIME_RELAY_XREAD_BLOCK", cfg.ReadBlock)
	cfg.ReplayGrace = envDuration("REALTIME_RELAY_REPLAY_GRACE", cfg.ReplayGrace)
	cfg.TrimHorizon = envDuration("REALTIME_RELAY_TRIM_HORIZON", 2*cfg.ReplayGrace)
	cfg.StreamTTL = envDuration("REALTIME_RELAY_STREAM_TTL", cfg.TrimHorizon+cfg.ReplayGrace)
	cfg.TTLRefreshInterval = envDuration("REALTIME_RELAY_TTL_REFRESH_INTERVAL", cfg.TTLRefreshInterval)
	cfg.MaintenanceInterval = envDuration("REALTIME_RELAY_MAINTENANCE_INTERVAL", cfg.MaintenanceInterval)
	cfg.StreamTTLEnabled = envBool("REALTIME_RELAY_STREAM_TTL_ENABLED", false)
	if err := cfg.Validate(); err != nil {
		slog.Warn("invalid realtime relay retention config; normalizing to safe values", "error", err)
	}
	return cfg.Normalized()
}

func realtimeRelayModeFromEnv() string {
	const defaultMode = "sharded"
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("REALTIME_RELAY_MODE")))
	if raw == "" {
		return defaultMode
	}
	switch raw {
	case "sharded", "dual", "legacy":
		return raw
	default:
		slog.Warn(msgInvalidEnvVarDefault, "name", "REALTIME_RELAY_MODE", "value", raw, "default", defaultMode)
		return defaultMode
	}
}

func envPositiveInt(name string, def int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		slog.Warn(msgInvalidEnvVarDefault, "name", name, "value", raw, "default", def, "error", err)
		return def
	}
	return v
}

func envNonNegativeInt(name string, def int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		slog.Warn(msgInvalidEnvVarDefault, "name", name, "value", raw, "default", def, "error", err)
		return def
	}
	return v
}

// maxLLMRetriesLimit caps MULTICA_LLM_MAX_RETRIES. The ceiling is a latency
// budget, not a taste call: SDK backoff is 0.5s doubling to an 8s cap, so 6
// retries spend ~21s and 10 spend ~48s sleeping before the last attempt. Every
// internal caller of pkg/llm runs under a far tighter deadline (8s for chat
// quick actions, 20s for title generation), so a budget past 5 cannot finish —
// it only converts a retryable upstream failure into a deadline-exceeded one.
const maxLLMRetriesLimit = 5

// parseLLMMaxRetries turns the raw MULTICA_LLM_MAX_RETRIES value into the
// tri-state llm.Config.MaxRetries expects: nil for unset (use the default),
// llm.Retries(0) to disable retries, llm.Retries(N) for a ceiling of N.
//
// Unlike the envFooInt helpers above it returns an error instead of warning and
// falling back to a default. A retry budget silently corrected to something the
// operator did not ask for is the failure this knob exists to remove
// (MUL-6364): a typo'd "3x" or a negative must stop the boot, not quietly
// restore the default and look configured.
func parseLLMMaxRetries(raw string) (*llm.RetryOverride, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("must be an integer, got %q", raw)
	}
	if v > maxLLMRetriesLimit {
		return nil, fmt.Errorf("must be at most %d, got %d", maxLLMRetriesLimit, v)
	}
	// llm.Retries owns the lower bound. It is the boundary that makes a negative
	// budget unrepresentable, and this is the only place the server builds one,
	// so the deployment-specific ceiling above and the type-level floor here
	// cannot disagree.
	override, err := llm.Retries(v)
	if err != nil {
		return nil, fmt.Errorf("must not be negative, got %d (use 0 to disable retries)", v)
	}
	return override, nil
}

func envPositiveInt64(name string, def int64) int64 {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		slog.Warn(msgInvalidEnvVarDefault, "name", name, "value", raw, "default", def, "error", err)
		return def
	}
	return v
}

func envDuration(name string, def time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v <= 0 {
		slog.Warn(msgInvalidEnvVarDefault, "name", name, "value", raw, "default", def.String(), "error", err)
		return def
	}
	return v
}

func envNonNegativeDuration(name string, def time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v < 0 {
		slog.Warn(msgInvalidEnvVarDefault, "name", name, "value", raw, "default", def.String(), "error", err)
		return def
	}
	return v
}

func holdBeforeShutdown(sig os.Signal, signals <-chan os.Signal, duration time.Duration) {
	if duration <= 0 {
		return
	}
	slog.Info("termination signal received; holding before shutdown",
		"signal", sig.String(),
		"duration", duration.String(),
	)
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-timer.C:
		slog.Info("shutdown hold complete", "duration", duration.String())
	case interruptSig := <-signals:
		slog.Info("shutdown hold interrupted by signal",
			"signal", interruptSig.String(),
			"configured_duration", duration.String(),
		)
	}
}

func envBool(name string, def bool) bool {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		slog.Warn(msgInvalidEnvVarDefault, "name", name, "value", raw, "default", def, "error", err)
		return def
	}
	return v
}

func backgroundServices(h *handler.Handler) (*service.TaskService, *service.AutopilotService) {
	return h.TaskService, h.AutopilotService
}

// jwtSecretBootError returns a non-nil error when the combination of
// JWT_SECRET and APP_ENV is unsafe to boot with: production must never run
// on an empty or publicly-known default secret (auth.ValidateJWTSecret).
// Non-production keeps the historical dev fallback (see auth.JWTSecret)
// and only warns.
func jwtSecretBootError(jwtSecret, appEnv string) error {
	isProduction := strings.EqualFold(strings.TrimSpace(appEnv), "production")
	if !isProduction {
		return nil
	}
	return auth.ValidateJWTSecret(jwtSecret)
}

type redisInfra struct {
	storeRedis        *redis.Client
	channelLeaseRedis *redis.Client
	relayWriteRedis   *redis.Client
	relayReadRedis    *redis.Client
	shardedReadRedis  *redis.Client
	legacyReadRedis   *redis.Client
	relay             realtime.ManagedRelay
	broadcaster       realtime.Broadcaster
	daemonWakeup      service.TaskWakeupNotifier
}

func (r *redisInfra) close(cancel context.CancelFunc) {
	if r.relay != nil {
		r.relay.Stop()
	}
	cancel()
	if r.relay != nil {
		r.relay.Wait()
	}
	closeRedisClient("realtime-read-legacy", r.legacyReadRedis)
	closeRedisClient("realtime-read-sharded", r.shardedReadRedis)
	closeRedisClient(scopeRealtimeRead, r.relayReadRedis)
	closeRedisClient("realtime-write", r.relayWriteRedis)
	closeRedisClient("channel-lease", r.channelLeaseRedis)
	closeRedisClient("store", r.storeRedis)
}

type serverMetrics struct {
	server              *http.Server
	httpMetrics         *obsmetrics.HTTPMetrics
	businessMetrics     *obsmetrics.BusinessMetrics
	channelMediaMetrics *obsmetrics.ChannelMediaReconcilerMetrics
	channelLeaseMetrics *obsmetrics.ChannelLeaseMetrics
	wecomMetrics        *obsmetrics.WecomMetrics
	samplerPool         *pgxpool.Pool
	addr                string
}

func (m *serverMetrics) close() {
	if m.samplerPool != nil {
		m.samplerPool.Close()
	}
}

func validateStartupConfig() {
	if err := jwtSecretBootError(os.Getenv("JWT_SECRET"), os.Getenv("APP_ENV")); err != nil {
		slog.Error(
			"refusing to start: "+err.Error()+
				"; generate a strong secret with `openssl rand -hex 32` and set JWT_SECRET (see .env.example)",
			"app_env", os.Getenv("APP_ENV"),
		)
		os.Exit(1)
	}
	if os.Getenv("JWT_SECRET") == "" {
		slog.Warn("JWT_SECRET is not set — using insecure dev default (allowed only because APP_ENV is not production).")
	}
	if os.Getenv("RESEND_API_KEY") == "" && strings.TrimSpace(os.Getenv("SMTP_HOST")) == "" {
		slog.Warn("no email backend configured (RESEND_API_KEY and SMTP_HOST both empty) — verification codes will be printed to the log instead of emailed.")
	}
	if os.Getenv("MULTICA_DEV_VERIFICATION_CODE") != "" {
		if strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production") {
			slog.Warn("MULTICA_DEV_VERIFICATION_CODE is set but ignored because APP_ENV=production.")
		} else {
			slog.Warn("MULTICA_DEV_VERIFICATION_CODE is enabled. Use it only for local development or private test instances.")
		}
	}
}

func initDatabase(ctx context.Context, dbURL string) *pgxpool.Pool {
	pool, err := newDBPool(ctx, dbURL)
	if err != nil {
		slog.Error("unable to connect to database", "error", err)
		os.Exit(1)
	}
	if err := pool.Ping(ctx); err != nil {
		slog.Error("unable to ping database", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to database")
	logPoolConfig(pool)
	return pool
}

func setupRedisRelay(relayCtx context.Context, hub *realtime.Hub, daemonHub *daemonws.Hub) *redisInfra {
	infra := &redisInfra{
		broadcaster:  hub,
		daemonWakeup: daemonHub,
	}

	sharedRedisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	relayRedisURL := realtimeRelayRedisURLFromEnv()
	if (sharedRedisURL != "" || relayRedisURL != "") && envBool("REDIS_DISABLE_CLIENT_NAME", false) {
		slog.Info("redis: CLIENT SETNAME disabled (REDIS_DISABLE_CLIENT_NAME=true) for managed Redis compatibility")
	}
	if sharedRedisURL != "" {
		if opts, err := redis.ParseURL(sharedRedisURL); err != nil {
			slog.Error("invalid REDIS_URL — request-path Redis features disabled", "error", err)
		} else {
			infra.storeRedis = newNamedRedisClient(opts, "store")
		}
	}
	if relayRedisURL != "" {
		setupRealtimeRelay(relayCtx, hub, daemonHub, infra, relayRedisURL)
	} else {
		slog.Info("realtime: REDIS_URL and REALTIME_RELAY_REDIS_URL are unset — using in-memory hub (single-node mode)")
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("CHANNEL_WS_LEASE_BACKEND")), "redis") {
		leaseRedisURL := channelLeaseRedisURLFromEnv()
		if leaseRedisURL == "" {
			slog.Error("channel leases: CHANNEL_WS_LEASE_REDIS_URL and REDIS_URL are unset")
		} else if opts, err := redis.ParseURL(leaseRedisURL); err != nil {
			slog.Error("channel leases: invalid Redis URL; supervisor will fail closed", "error", err)
		} else {
			infra.channelLeaseRedis = newNamedRedisClient(opts, "channel-lease")
		}
	}

	return infra
}

func setupRealtimeRelay(relayCtx context.Context, hub *realtime.Hub, daemonHub *daemonws.Hub, infra *redisInfra, relayRedisURL string) {
	opts, err := redis.ParseURL(relayRedisURL)
	if err != nil {
		slog.Error("invalid realtime relay Redis URL — falling back to in-memory hub", "error", err)
		return
	}

	infra.relayWriteRedis = newNamedRedisClient(opts, "realtime-write")
	relayMode := realtimeRelayModeFromEnv()
	relayConfig := shardedRelayConfigFromEnv()
	switch relayMode {
	case "legacy":
		infra.relayReadRedis = newNamedRedisClient(opts, scopeRealtimeRead)
		infra.relay = realtime.NewRedisRelayWithClientsAndConfig(hub, infra.relayWriteRedis, infra.relayReadRedis, relayConfig.RetentionConfig())
		slog.Info("daemon websocket wakeup: Redis fanout disabled in legacy realtime relay mode")
	case "dual":
		infra.shardedReadRedis = newNamedRedisClient(opts, "realtime-read-sharded")
		infra.legacyReadRedis = newNamedRedisClient(opts, "realtime-read-legacy")
		sharded := realtime.NewShardedStreamRelay(hub, infra.relayWriteRedis, infra.shardedReadRedis, relayConfig)
		sharded.SetDaemonRuntimeDeliverer(daemonHub)
		legacy := realtime.NewRedisRelayWithClientsAndConfig(hub, infra.relayWriteRedis, infra.legacyReadRedis, relayConfig.RetentionConfig())
		infra.relay = realtime.NewMirroredRelay(sharded, legacy)
		infra.daemonWakeup = daemonws.NewRelayNotifier(daemonHub, sharded)
	default:
		infra.relayReadRedis = newNamedRedisClient(opts, scopeRealtimeRead)
		sharded := realtime.NewShardedStreamRelay(hub, infra.relayWriteRedis, infra.relayReadRedis, relayConfig)
		sharded.SetDaemonRuntimeDeliverer(daemonHub)
		infra.relay = sharded
		infra.daemonWakeup = daemonws.NewRelayNotifier(daemonHub, sharded)
	}
	infra.relay.Start(relayCtx)
	infra.broadcaster = realtime.NewDualWriteBroadcaster(hub, infra.relay)
	storePoolSize := 0
	if infra.storeRedis != nil {
		storePoolSize = infra.storeRedis.Options().PoolSize
	}
	slog.Info(
		"realtime: Redis relay enabled",
		"node_id", infra.relay.NodeID(),
		"mode", relayMode,
		"dedicated_instance", strings.TrimSpace(os.Getenv("REALTIME_RELAY_REDIS_URL")) != "",
		"shards", relayConfig.Shards,
		"stream_max_len", relayConfig.StreamMaxLen,
		"replay_grace", relayConfig.ReplayGrace.String(),
		"trim_horizon", relayConfig.TrimHorizon.String(),
		"stream_ttl", relayConfig.StreamTTL.String(),
		"stream_ttl_enabled", relayConfig.StreamTTLEnabled,
		"xread_count", relayConfig.ReadCount,
		"xread_block", relayConfig.ReadBlock.String(),
		"store_pool_size", storePoolSize,
		"realtime_write_pool_size", opts.PoolSize,
		"realtime_read_pool_size", opts.PoolSize,
	)
}

func setupMetrics(ctx context.Context, pool *pgxpool.Pool, daemonHub *daemonws.Hub, dbURL string) *serverMetrics {
	metricsConfig := obsmetrics.ConfigFromEnv()
	if !metricsConfig.Enabled() {
		return &serverMetrics{}
	}

	samplerPool, err := newSamplerDBPool(ctx, dbURL)
	if err != nil {
		slog.Warn("metrics: failed to build sampler pgxpool; sampler disabled", "error", err)
		samplerPool = nil
	}

	metricsRegistry := obsmetrics.NewRegistry(obsmetrics.RegistryOptions{
		Pool:     pool,
		Realtime: realtime.M,
		DaemonWS: daemonws.M,
		Version:  version,
		Commit:   commit,
		BusinessSampler: func() *obsmetrics.BusinessSamplerOptions {
			if samplerPool == nil {
				return nil
			}
			return &obsmetrics.BusinessSamplerOptions{Pool: samplerPool}
		}(),
	})

	if daemonHub != nil {
		daemonHub.SetMessageKindRecorder(metricsRegistry.Business)
	}
	metricsServer := obsmetrics.NewServer(metricsConfig.Addr, metricsRegistry.Gatherer)
	if !obsmetrics.IsLoopbackAddr(metricsConfig.Addr) {
		slog.Warn(
			"metrics listener is not loopback-only; restrict access with private networking, allowlists, or proxy auth",
			"addr", metricsConfig.Addr,
		)
	}

	return &serverMetrics{
		server:              metricsServer,
		httpMetrics:         metricsRegistry.HTTP,
		businessMetrics:     metricsRegistry.Business,
		channelMediaMetrics: metricsRegistry.ChannelMedia,
		channelLeaseMetrics: metricsRegistry.ChannelLease,
		wecomMetrics:        metricsRegistry.Wecom,
		samplerPool:         samplerPool,
		addr:                metricsConfig.Addr,
	}
}

func startBackgroundWorkers(
	sweepCtx, autopilotCtx context.Context,
	pool *pgxpool.Pool,
	queries *db.Queries,
	bus *events.Bus,
	h *handler.Handler,
	storeRedis *redis.Client,
	heartbeatScheduler *handler.BatchedHeartbeatScheduler,
	channelMediaMetrics *obsmetrics.ChannelMediaReconcilerMetrics,
) {
	taskSvc, autopilotSvc := backgroundServices(h)
	registerAutopilotListeners(bus, autopilotSvc)

	var liveness handler.LivenessStore = handler.NewNoopLivenessStore()
	if storeRedis != nil {
		liveness = handler.NewRedisLivenessStore(storeRedis)
	}

	runtimeReconnectGrace := envDuration("MULTICA_RUNTIME_RECONNECT_GRACE", defaultRuntimeReconnectGrace)
	if runtimeReconnectGrace < minimumRuntimeReconnectGrace {
		slog.Warn("runtime reconnect grace is shorter than heartbeat freshness; clamping",
			"configured", runtimeReconnectGrace,
			"minimum", minimumRuntimeReconnectGrace,
		)
		runtimeReconnectGrace = minimumRuntimeReconnectGrace
	}
	go runRuntimeSweeper(sweepCtx, pool, queries, liveness, taskSvc, bus, runtimeReconnectGrace)
	go heartbeatScheduler.Run(sweepCtx)
	go runAutopilotFailureMonitor(autopilotCtx, queries, bus, envFailureMonitorConfig())
	if autopilotSvc.QuotaEnabled() {
		go runAutopilotQuotaReconciler(autopilotCtx, autopilotSvc)
	}
	go runDBStatsLogger(sweepCtx, pool)
	if h.WebhookDeliveryWorker != nil {
		go h.WebhookDeliveryWorker.Run(sweepCtx)
	}
	if h.TelegramOutbound != nil {
		h.TelegramOutbound.Start(sweepCtx)
	}
	h.PRRefresh.Start(sweepCtx)

	if h.ChannelSupervisor != nil {
		go h.ChannelSupervisor.Run(sweepCtx)
	}
	if h.ChannelMediaReconciler != nil {
		h.ChannelMediaReconciler.Metrics = channelMediaMetrics
		go h.ChannelMediaReconciler.Run(sweepCtx)
	}

	schedulerMgr := scheduler.NewManager(pool, scheduler.Options{})
	if err := schedulerMgr.Register(scheduler.TaskUsageHourlyJob(pool)); err != nil {
		slog.Warn("scheduler: failed to register task_usage_hourly rollup job", "error", err)
	}
	if err := schedulerMgr.Register(scheduler.AutopilotScheduleDispatchJob(pool, queries, autopilotSvc)); err != nil {
		slog.Warn("scheduler: failed to register autopilot_schedule_dispatch job", "error", err)
	}
	go func() {
		_ = schedulerMgr.Run(sweepCtx)
	}()
}

func drainChannelSupervisor(h *handler.Handler) {
	if h.ChannelSupervisor == nil {
		return
	}
	if !h.ChannelSupervisor.WaitWithTimeout(h.ChannelSupervisor.ShutdownTimeout()) {
		slog.Warn("channel supervisor: connections did not exit within shutdown timeout; proceeding",
			"timeout", h.ChannelSupervisor.ShutdownTimeout().String(),
		)
	}
	if h.ChannelRouter != nil {
		drainCtx, drainCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if !h.ChannelRouter.Drain(drainCtx) {
			slog.Warn("channel router: drain deadline reached; deferred media fallback remains durable")
		}
		drainCancel()
	}
}

func gracefulShutdown(
	srv *http.Server,
	metricsServer *http.Server,
	profilingServer *http.Server,
	h *handler.Handler,
	heartbeatScheduler *handler.BatchedHeartbeatScheduler,
	autopilotCancel context.CancelFunc,
	sweepCancel context.CancelFunc,
) {
	slog.Info("shutting down server")
	autopilotCancel()

	apiShutdownCtx, apiShutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := srv.Shutdown(apiShutdownCtx); err != nil {
		apiShutdownCancel()
		slog.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}
	apiShutdownCancel()

	sweepCancel()
	heartbeatScheduler.Stop()
	if h.WebhookDeliveryWorker != nil && !h.WebhookDeliveryWorker.WaitWithTimeout(5*time.Second) {
		slog.Warn("webhook delivery worker did not exit within shutdown timeout")
	}
	if h.TelegramOutbound != nil && !h.TelegramOutbound.WaitWithTimeout(5*time.Second) {
		slog.Warn("telegram outbound workers did not exit within shutdown timeout")
	}

	drainChannelSupervisor(h)

	if metricsServer != nil {
		metricsShutdownCtx, metricsShutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := metricsServer.Shutdown(metricsShutdownCtx); err != nil {
			slog.Error("metrics server forced to shutdown", "error", err)
		}
		metricsShutdownCancel()
	}
	profilingShutdownCtx, profilingShutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := profilingServer.Shutdown(profilingShutdownCtx); err != nil {
		slog.Error("pprof server forced to shutdown", "error", err)
	}
	profilingShutdownCancel()
	slog.Info("server stopped")
}

func main() {
	logger.Init()
	validateStartupConfig()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	shutdownHoldDuration := envNonNegativeDuration("MULTICA_SHUTDOWN_HOLD_DURATION", 0)

	flags, err := featureflag.NewServiceFromEnv(featureflag.WithLogger(slog.Default()))
	if err != nil {
		slog.Error("feature flag configuration failed to load", "error", err)
		os.Exit(1)
	}
	_ = flags

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	ctx := context.Background()
	pool := initDatabase(ctx, dbURL)
	defer pool.Close()

	bus := events.New()
	hub := realtime.NewHub()
	go hub.Run()
	daemonHub := daemonws.NewHub()

	relayCtx, relayCancel := context.WithCancel(context.Background())
	redisInfra := setupRedisRelay(relayCtx, hub, daemonHub)
	defer redisInfra.close(relayCancel)

	registerListeners(bus, redisInfra.broadcaster)

	analyticsClient := analytics.NewFromEnv()
	defer analyticsClient.Close()

	queries := db.New(pool)
	hub.SetAuthorizer(newScopeAuthorizer(queries))
	registerSubscriberListeners(bus, pool)
	registerActivityListeners(bus, queries)
	registerNotificationListeners(bus, queries)

	metrics := setupMetrics(ctx, pool, daemonHub, dbURL)
	defer metrics.close()

	heartbeatScheduler := handler.NewBatchedHeartbeatScheduler(queries, handler.DefaultHeartbeatBatchInterval)

	llmMaxRetries, err := parseLLMMaxRetries(os.Getenv("MULTICA_LLM_MAX_RETRIES"))
	if err != nil {
		slog.Error("invalid MULTICA_LLM_MAX_RETRIES", "error", err)
		os.Exit(1)
	}

	r, h := NewRouterWithOptions(pool, hub, bus, analyticsClient, redisInfra.storeRedis, RouterOptions{
		HTTPMetrics:         metrics.httpMetrics,
		BusinessMetrics:     metrics.businessMetrics,
		ChannelLeaseMetrics: metrics.channelLeaseMetrics,
		ChannelLeaseRedis:   redisInfra.channelLeaseRedis,
		WecomMetrics:        metrics.wecomMetrics,
		DaemonHub:           daemonHub,
		DaemonWakeup:        redisInfra.daemonWakeup,
		FeatureFlags:        flags,
		HeartbeatScheduler:  heartbeatScheduler,
		LLMMaxRetries:       llmMaxRetries,
	})

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}
	profilingServer := profiling.NewServer()

	sweepCtx, sweepCancel := context.WithCancel(context.Background())
	autopilotCtx, autopilotCancel := context.WithCancel(context.Background())

	startBackgroundWorkers(
		sweepCtx, autopilotCtx,
		pool, queries, bus, h,
		redisInfra.storeRedis,
		heartbeatScheduler,
		metrics.channelMediaMetrics,
	)

	if metrics.server != nil {
		go func() {
			slog.Info("metrics server starting", "addr", metrics.addr)
			if err := metrics.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("metrics server disabled after startup error", "error", err)
			}
		}()
	}

	go func() {
		slog.Info("pprof server starting", "addr", profilingServer.Addr)
		if err := profilingServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("pprof server disabled after startup error", "error", err)
		}
	}()

	go func() {
		slog.Info("server starting", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	holdBeforeShutdown(sig, quit, shutdownHoldDuration)
	signal.Stop(quit)

	gracefulShutdown(srv, metrics.server, profilingServer, h, heartbeatScheduler, autopilotCancel, sweepCancel)
}
