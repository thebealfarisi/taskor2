package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/integrations/channel"
	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
	composiointeg "github.com/multica-ai/multica/server/internal/integrations/composio"
	"github.com/multica-ai/multica/server/internal/integrations/dingtalk"
	"github.com/multica-ai/multica/server/internal/integrations/lark"
	"github.com/multica-ai/multica/server/internal/integrations/slack"
	"github.com/multica-ai/multica/server/internal/integrations/telegram"
	"github.com/multica-ai/multica/server/internal/integrations/wecom"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/storage"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	composiosdk "github.com/multica-ai/multica/server/pkg/composio"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func initServerIntegrations(
	h *handler.Handler,
	queries *db.Queries,
	pool *pgxpool.Pool,
	bus *events.Bus,
	store storage.Storage,
	rdb *redis.Client,
	daemonHub *daemonws.Hub,
	opts RouterOptions,
	signupConfig handler.Config,
) (*auth.PATCache, *auth.DaemonTokenCache, *auth.CloudPATVerifier) {
	// Channel engine (MUL-3620): the platform-agnostic inbound runtime.
	// Built UNCONDITIONALLY — it drives any channel.Channel, not just
	// Feishu, so it must not depend on the Lark master key (a future
	// Slack-only deployment has no Lark key). Platform adapters register a
	// Factory + ResolverSet into it below; the Supervisor enumerates active
	// installations across ALL channel types and routes each to its
	// registered platform's Factory. Installations whose channel_type has no
	// registered Factory are skipped by the Supervisor — either no platform is
	// configured, or (Slack/B2) the platform drives ONE deployment-level
	// connection of its own outside the per-installation supervisor. The Router
	// is the single shared inbound handler injected into every Channel.
	channelRegistry := channel.NewRegistry()
	channelRouter := engine.NewRouter(h.IssueService, h.TaskService, queries, engine.RouterConfig{Logger: slog.Default()})
	// Debounce the per-session run trigger so a burst of messages collapses
	// into one agent run instead of one per message (MUL-2968).
	channelRouter.EnableRunBatching(engine.DefaultChatRunBatchWindow)
	h.ChannelRouter = channelRouter
	// Media intent-ledger reconciler: settles uploaded-but-unbound objects.
	// Built ONLY when a storage backend exists — store is nil when S3 is not
	// configured and the local upload dir failed to initialize, and a
	// reconciler with nil Storage would panic the worker goroutine on the
	// first unreferenced row (ledger rows can pre-exist from a boot where
	// storage WAS configured). Without storage the resolver skips every
	// upload, so no new rows appear and the ledger simply waits for a boot
	// with working storage. Started from main.go as its own worker.
	if store != nil {
		h.ChannelMediaReconciler = &service.ChannelMediaReconciler{
			Queries: queries,
			Storage: store,
			Logger:  slog.Default(),
		}
	}
	installationStore := lark.NewChannelInstallationStore(queries)
	h.ChannelSupervisor = buildChannelSupervisor(
		installationStore,
		installationStore,
		channelRegistry,
		channelRouter.Handle,
		opts,
	)

	// Lark integration. Only wired when MULTICA_LARK_SECRET_KEY is set:
	// the InstallationService refuses to fall back to plaintext storage
	// for app_secret, and the BindingTokenService cannot mint usable
	// tokens without it either. When the key is absent the Lark
	// handlers return 503 with a clear message; the rest of the server
	// continues to start so self-host deployments that have not opted
	// in to Lark are unaffected. Feishu registers its Factory + ResolverSet
	// into the channel engine above.
	if larkKey, err := secretbox.LoadKey("MULTICA_LARK_SECRET_KEY"); err == nil {
		box, err := secretbox.New(larkKey)
		if err != nil {
			slog.Error("lark: secretbox.New failed; lark integration disabled", "error", err)
		} else {
			installSvc, err := lark.NewInstallationService(queries, box)
			if err != nil {
				slog.Error("lark: InstallationService init failed; lark integration disabled", "error", err)
			} else {
				h.LarkInstallations = installSvc
				h.LarkBindingTokens = lark.NewBindingTokenService(queries, pool)
				slog.Info("lark integration enabled")

				// APIClient: wire the real Lark Open Platform HTTP client
				// (IM v1 send/patch + binding-prompt + bot info). Setting
				// MULTICA_LARK_SECRET_KEY is the operator's opt-in for
				// the integration as a whole; we don't expose a separate
				// "HTTP enabled" knob because the inbound dispatcher
				// without outbound replies is not a useful production
				// state, and CI / integration tests that want to avoid
				// real Lark traffic can point MULTICA_LARK_HTTP_BASE_URL
				// at a mock server.
				//
				// MULTICA_LARK_HTTP_BASE_URL is an OPTIONAL deployment-wide
				// override. Normal operation leaves it empty: each call then
				// resolves its open-platform host from the installation's
				// region (open.feishu.cn vs open.larksuite.com), so one
				// deployment serves both clouds. Set it only to force every
				// installation onto one host — a proxy, a mock for tests, or
				// a single-cloud staging setup.
				larkClient := lark.NewHTTPAPIClient(lark.HTTPClientConfig{
					BaseURL: strings.TrimSpace(os.Getenv("MULTICA_LARK_HTTP_BASE_URL")),
					Logger:  slog.Default(),
				})
				h.LarkAPIClient = larkClient

				// Channel-backed store: routes the lark package's DB seams
				// onto the channel_* tables (MUL-3515). Interface-wired
				// consumers (patcher, typing indicator, dispatcher, hub,
				// backfills) take it directly; the constructor-based services
				// wrap *db.Queries internally, so they keep taking queries.
				cs := lark.NewChannelStore(queries)
				patcher := lark.NewPatcher(cs, installSvc, larkClient, lark.PatcherConfig{})
				patcher.Register(bus)

				// Typing indicator: shows a "processing" reaction on the user's
				// message while the agent is working, then removes it before the
				// reply is sent. Best-effort; failures are logged only.
				typingIndicator := lark.NewTypingIndicatorManager(larkClient, installSvc, cs, slog.Default())
				patcher.SetTypingIndicatorManager(typingIndicator)

				// Inbound pipeline seams: lark_inbound_audit logger and the
				// shared channel-agnostic chat-session service. They back the
				// Feishu ResolverSet that the engine.Router runs through,
				// sharing the same IssueService + TaskService that back HTTP, so
				// /issue-created issues share counter, dup guard, project
				// boundary, broadcast, analytics and agent-enqueue with the rest
				// of the product. Feishu is just another consumer of the shared
				// engine.ChatSession (channel_type-keyed); the Lark session
				// titles preserve the pre-cutover wording.
				auditLogger := lark.NewAuditLogger(queries)
				feishuSession := engine.NewChatSession(queries, pool, channel.TypeFeishu, engine.SessionTitles{
					Group:    "Lark group chat",
					Direct:   "Lark direct message",
					Fallback: "Lark chat",
				})

				// OutcomeReplier wires the outbound side: NeedsBinding /
				// AgentOffline / AgentArchived / issue-created translate to a
				// Lark-side reply card. Requires the real APIClient and the
				// binding token service; otherwise it falls back to the noop
				// replier (outcomes logged, not delivered). We only register
				// it on the ResolverSet when it can actually deliver, so a
				// pre-outbound deployment pays no reply-goroutine cost.
				replier := lark.NewLarkOutcomeReplier(lark.OutcomeReplierConfig{
					APIClient:   larkClient,
					BindingSvc:  h.LarkBindingTokens,
					Credentials: installSvc,
					Queries:     queries,
					AppURL:      appURLFromEnv(),
					Logger:      slog.Default(),
				})
				var resolverReplier lark.OutcomeReplier
				if larkClient.IsConfigured() {
					resolverReplier = replier
				}

				// Feishu adapter (MUL-3620): the WSLongConnConnector talks
				// Lark's long-conn protocol over gorilla/websocket and wraps
				// every read with a ctx-cancel watchdog so lease loss /
				// shutdown breaks the blocking ReadMessage in bounded time —
				// the invariant §4.4 leans on. If the endpoint fetcher fails
				// to initialize (bad MULTICA_LARK_CALLBACK_BASE_URL or
				// similar), buildLarkConnector logs and falls back to the
				// NoopConnector so the lease / supervisor lifecycle still runs
				// against real DB rows — inbound messages are silently dropped
				// until the config is fixed, with the boot log labelling the
				// mode "noop".
				//
				// Registering the Factory (connect/send) + ResolverSet
				// (inbound pipeline seams) is all it takes to add the platform
				// to the engine — no engine edit.
				connector, connectorLabel := buildLarkConnector(installSvc, larkClient)
				lark.RegisterFeishu(channelRegistry, lark.FeishuChannelDeps{
					Connector:   connector,
					APIClient:   larkClient,
					Credentials: installSvc,
					Logger:      slog.Default(),
				})
				mediaResolver := lark.NewFeishuMediaResolver(larkClient, installSvc, store, engine.NewDBMediaIntentLedger(queries), slog.Default())
				channelRouter.Register(channel.TypeFeishu, lark.NewFeishuResolverSet(
					cs, feishuSession, auditLogger, resolverReplier, typingIndicator, mediaResolver,
				))
				slog.Info("lark inbound pipeline wired", "connector", connectorLabel)

				// One-shot union_id backfill for installations created
				// before migration 112 added bot_union_id. Runs off the
				// hot startup path so a slow Lark round-trip cannot block
				// HTTP listener boot. New installs already write
				// bot_union_id during the device-flow finalize, so this
				// is bridge code — it will simply find no rows to update
				// on a fresh deployment and exit. MUL-2671.
				go lark.BackfillBotUnionIDs(context.Background(), cs, larkClient, installSvc, slog.Default())

				// Upgrade repair for deployments that ran the whole
				// integration against Lark international via the deployment-
				// wide base-URL override before per-installation region
				// existed: migration 116 backfilled their rows to 'feishu',
				// so relabel them to 'lark' (their true cloud) before the
				// operator clears the override. No-op on mainland / fresh
				// deployments. Off the hot startup path like the union_id
				// backfill. MUL-3083.
				go lark.BackfillRegionFromLegacyOverride(context.Background(), cs,
					strings.TrimSpace(os.Getenv("MULTICA_LARK_HTTP_BASE_URL")),
					strings.TrimSpace(os.Getenv("MULTICA_LARK_CALLBACK_BASE_URL")),
					slog.Default())

				// Device-flow registration service: end-to-end install
				// pipeline that talks to accounts.feishu.cn (RFC 8628)
				// for the QR-scan handshake and then commits the
				// resulting Bot credentials + the installer's
				// lark_user_binding in one DB transaction. The optional
				// MULTICA_LARK_REGISTRATION_DOMAIN / _LARK_DOMAIN env
				// vars override the protocol hosts for staging / dev.
				regCfg := lark.RegistrationConfig{
					Domain:     strings.TrimSpace(os.Getenv("MULTICA_LARK_REGISTRATION_DOMAIN")),
					LarkDomain: strings.TrimSpace(os.Getenv("MULTICA_LARK_REGISTRATION_LARK_DOMAIN")),
				}
				regClient := lark.NewRegistrationClient(regCfg)
				regSvc, rerr := lark.NewRegistrationService(
					lark.RegistrationServiceConfig{Logger: slog.Default()},
					regClient,
					larkClient,
					queries,
					pool,
					installSvc,
					h.LarkBindingTokens,
				)
				if rerr != nil {
					slog.Error("lark: RegistrationService init failed; install disabled", "error", rerr)
				} else {
					// Publish lark_installation:created at row-commit time so the
					// connection badge refreshes on every workspace client, not just
					// the tab that polls the install status to success.
					regSvc.SetEventBus(bus)
					h.LarkRegistration = regSvc
					slog.Info("lark device-flow install enabled")
				}
			}
		}
	} else {
		slog.Info("lark integration disabled (MULTICA_LARK_SECRET_KEY not set)")
	}

	// Slack integration. Multi-tenant B2 model (MUL-3666): Multica hosts ONE
	// Slack app, workspaces self-install via OAuth, and inbound runs on a single
	// deployment-level Socket Mode connection routed by team_id — replacing the
	// stage-3 per-installation connection model (MUL-3516).
	//
	// Two deployment-level env vars gate the two halves:
	//   - MULTICA_SLACK_SECRET_KEY decrypts the per-installation bot token
	//     (xoxb-) stored on the channel_installation row. It gates the inbound
	//     ResolverSet + the outbound reply subscriber, so without it there is no
	//     Slack at all.
	//   - MULTICA_SLACK_APP_TOKEN is the app-level token (xapp-) authorizing the
	//     single Socket Mode connection. It cannot be obtained via OAuth, so it
	//     is a one-time operator config. Without it, inbound is disabled (the
	//     ResolverSet + outbound are still wired so an existing install's replies
	//     keep flowing, but no new events are received).
	//
	// The ResolverSet/Outbound share the same engine.ChatSession, channel_*
	// tables, IssueService and TaskService as Feishu, so /issue, dedup, and
	// run-triggering behave identically. Feishu is untouched. Each Slack
	// installation is a bring-your-own-app (BYO) install carrying its OWN
	// app-level token, so a per-installation Slack Factory is registered and the
	// Supervisor drives one Socket Mode connection per installation (like Feishu).
	if slackKey, err := secretbox.LoadKey("MULTICA_SLACK_SECRET_KEY"); err == nil {
		box, err := secretbox.New(slackKey)
		if err != nil {
			slog.Error("slack: secretbox.New failed; slack integration disabled", "error", err)
		} else {
			// Outbound replier (MUL-3666): delivers NeedsBinding prompt /
			// AgentOffline / AgentArchived / issue-created notices. The binding
			// token service mints the single-use token embedded in the prompt's
			// redeem link; the redeem endpoint (registered below, public) binds
			// the Slack user to their Multica account.
			slackBindingSvc := slack.NewBindingTokenService(queries, pool)
			h.SlackBindingTokens = slackBindingSvc
			slackReplier := slack.NewOutboundReplier(slack.OutboundReplierConfig{
				Binding: slackBindingSvc,
				Decrypt: box.Open,
				// The bind link (/slack/bind) is a web-app page, so it must use the
				// app URL (MULTICA_APP_URL ?? FRONTEND_ORIGIN), NOT MULTICA_PUBLIC_URL
				// (the backend/API URL). Mirrors the Lark replier (appURLFromEnv).
				AppURL: appURLFromEnv(),
				Logger: slog.Default(),
			})
			// Typing indicator (MUL-3874): a 👀 reaction on the user's message
			// while the agent works, cleared when the run finishes or fails.
			// Best-effort; failures are logged only. Registered before the
			// outbound reply subscriber so, on EventChatDone, the reaction clears
			// ahead of the reply (bus delivery is synchronous, in subscription
			// order). Subscribing here is also the only path that clears the
			// reaction on a failed run, which the outbound replier does not handle.
			slackTyping := slack.NewTypingIndicatorManager(queries, box.Open, slog.Default())
			slackTyping.Register(bus)
			// Slack attachments require object storage because each chat
			// attachment points to an uploaded object. When storage is disabled,
			// leave the media resolver unset and ingest Slack messages as text.
			var slackMedia engine.MediaResolver
			if store != nil {
				slackMedia = slack.NewMediaResolver(
					box.Open,
					store,
					engine.NewDBMediaIntentLedger(queries),
					slog.Default(),
				)
			}
			channelRouter.Register(slack.TypeSlack, slack.NewSlackResolverSet(queries, pool, slackReplier, slackTyping, slackMedia))
			slack.NewOutbound(queries, box.Open, slog.Default()).Register(bus)

			// On-demand history reader behind the unified `multica chat history`
			// command (MUL-3871): pull the session's Slack conversation when the
			// agent asks, instead of force-assembling it on every inbound.
			h.SlackHistory = slack.NewHistory(queries, box.Open, slog.Default())

			// `/issue` slash command (MUL-3908): a real Slack slash command,
			// delivered over the same Socket Mode connection. It is a quick-create
			// entry point — the invoker's natural-language description is enqueued as
			// a quick-create task (no chat session or chat run) and the agent authors
			// the well-formed issue in the background — reusing the shared TaskService
			// + binding service. The invoker gets a private ephemeral acknowledgement
			// and a Multica notification when the issue lands.
			slackSlash := slack.NewSlashCommandProcessor(slack.SlashCommandConfig{
				Queries: queries,
				Tasks:   h.TaskService,
				Binding: slackBindingSvc,
				AppURL:  appURLFromEnv(),
				Logger:  slog.Default(),
			})

			// Per-installation inbound: the Supervisor builds + supervises one
			// Socket Mode connection per active Slack installation, authenticated
			// with that installation's OWN app-level token (xapp-, pasted at BYO
			// install) — no deployment-level app token, no single connection.
			slack.RegisterSlack(channelRegistry, slack.ChannelDeps{Decrypt: box.Open, Logger: slog.Default(), Slash: slackSlash})

			// BYO self-serve install (paste bot token + app-level token). The
			// InstallService needs only the at-rest encryption key — there is no
			// hosted OAuth client credential.
			installSvc, ierr := slack.NewInstallService(queries, pool, box, slog.Default())
			if ierr != nil {
				slog.Error("slack: InstallService init failed; install disabled", "error", ierr)
			} else {
				h.SlackInstall = installSvc
			}
			slog.Info("slack integration enabled (BYO per-installation socket mode)")
		}
	} else {
		slog.Info("slack integration disabled (MULTICA_SLACK_SECRET_KEY not set)")
	}

	// DingTalk uses one outbound Stream connection per BYO installation. The
	// AppSecret is encrypted at rest and the integration is inert unless its
	// dedicated deployment key is configured.
	if dingtalkKey, err := secretbox.LoadKey("MULTICA_DINGTALK_SECRET_KEY"); err == nil {
		box, err := secretbox.New(dingtalkKey)
		if err != nil {
			slog.Error("dingtalk: secretbox.New failed; integration disabled", "error", err)
		} else {
			dingtalkClient := dingtalk.NewClient(nil, "")
			bindingSvc := dingtalk.NewBindingTokenService(queries, pool)
			h.DingTalkBindingTokens = bindingSvc
			replier := dingtalk.NewOutboundReplier(dingtalk.OutboundReplierConfig{
				Binding: bindingSvc,
				Decrypt: box.Open,
				Client:  dingtalkClient,
				AppURL:  appURLFromEnv(),
				Logger:  slog.Default(),
			})
			ack := dingtalk.NewAckNotifier(dingtalkClient, box.Open, slog.Default())
			var media engine.MediaResolver
			if store != nil {
				media = dingtalk.NewMediaResolver(
					dingtalkClient,
					box.Open,
					store,
					engine.NewDBMediaIntentLedger(queries),
					slog.Default(),
				)
			}
			channelRouter.Register(dingtalk.TypeDingTalk, dingtalk.NewDingTalkResolverSet(queries, pool, replier, ack, media))
			dingtalk.NewOutbound(queries, box.Open, dingtalkClient, slog.Default()).Register(bus)
			dingtalk.RegisterDingTalk(channelRegistry, dingtalk.ChannelDeps{
				Decrypt: box.Open,
				Client:  dingtalkClient,
				Logger:  slog.Default(),
			})
			installSvc, installErr := dingtalk.NewInstallService(queries, pool, box, slog.Default())
			if installErr != nil {
				slog.Error("dingtalk: InstallService init failed; install disabled", "error", installErr)
			} else {
				h.DingTalkInstall = installSvc
			}
			slog.Info("dingtalk integration enabled (BYO per-installation stream mode)")
		}
	} else {
		slog.Info("dingtalk integration disabled (MULTICA_DINGTALK_SECRET_KEY not set)")
	}

	// WeCom smart-bot integration ("智能机器人" / aibot). Per-installation
	// WebSocket long connection to wss://openws.work.weixin.qq.com; the
	// Supervisor drives one connection per active wecom installation, gated
	// by the shared ws_lease_token so multi-replica deployments still hold
	// at most one active socket per bot (WeCom itself only permits one).
	//
	// Gated by MULTICA_WECOM_SECRET_KEY. Without it, the whole block is
	// skipped and the wecom Web-UI endpoints return 503; existing deployments
	// are unaffected. The smart-bot flow does NOT require any public HTTP
	// callback, so nothing else needs to be exposed to the internet.
	if wecomKey, err := secretbox.LoadKey("MULTICA_WECOM_SECRET_KEY"); err == nil {
		box, err := secretbox.New(wecomKey)
		if err != nil {
			slog.Error("wecom: secretbox.New failed; wecom integration disabled", "error", err)
		} else {
			credsResolver, err := wecom.NewSecretboxCredentialsResolver(box)
			if err != nil {
				slog.Error("wecom: credentials resolver init failed; wecom integration disabled", "error", err)
			} else {
				wecomStore := wecom.NewStore(queries)
				h.WecomStore = wecomStore
				h.WecomCredentials = credsResolver

				// Binding tokens back the per-user "link your Multica account"
				// prompt sent to first-time WeCom senders. aibot userids are
				// anonymized T-prefixed ids with no relation to real userids
				// or emails, so an explicit binding table is the only correct
				// answer — see wecom/binding.go for the rationale.
				wecomBinding := wecom.NewBindingTokenService(queries, pool)
				h.WecomBindingTokens = wecomBinding

				// Senders registry: the wecom OutboundReplier is created here
				// at boot, but the live wsSender it needs to push
				// aibot_send_msg only exists inside a running wecomChannel.
				// wecom.NewSendersRegistry mints a shared map; the
				// ChannelDeps write side and the Replier read side both
				// receive it, and each Channel.Connect self-registers on
				// entry and clears on exit.
				wecomSenders := wecom.NewSendersRegistry()

				wecomReplier := wecom.NewOutboundReplier(wecom.OutboundReplierConfig{
					Binding: wecomBinding,
					Senders: wecomSenders,
					AppURL:  appURLFromEnv(),
					Logger:  slog.Default(),
				})

				// Wecom shares the engine.ChatSession (channel_type-keyed) so
				// /issue, dedup, and run-triggering behave identically across
				// platforms. Session titles use the wecom-flavored wording
				// (Chinese product voice — wecom deployments are China-only).
				wecomSession := engine.NewChatSession(queries, pool, wecom.TypeWecom, engine.SessionTitles{
					Group:    "企业微信群聊",
					Direct:   "企业微信单聊",
					Fallback: "企业微信会话",
				})

				wecom.RegisterWecom(channelRegistry, wecom.ChannelDeps{
					Credentials: credsResolver,
					Senders:     wecomSenders,
					Metrics:     wecomMetricsOrNil(opts.WecomMetrics),
					Logger:      slog.Default(),
				})
				// Inbound media: a callback carries a pre-signed COS url and
				// a per-url key, so the resolver needs no WeCom credential —
				// only somewhere durable to put the bytes. Without an object
				// store there is nothing to point an attachment at, so the
				// resolver is left nil and attachments stay as their
				// placeholder text. Same nil-guard as DingTalk above.
				var wecomMedia engine.MediaResolver
				if store != nil {
					wecomMedia = wecom.NewMediaResolver(
						store,
						engine.NewDBMediaIntentLedger(queries),
						wecomSenders,
						slog.Default(),
					)
				}
				channelRouter.Register(wecom.TypeWecom, wecom.NewResolverSet(
					wecomStore, wecomSession, wecomReplier, wecomMedia,
				))

				// EventChatDone subscriber: pushes the agent's chat reply
				// back over the same aibot WebSocket the inbound loop owns.
				// Mirrors slack.NewOutbound(...).Register(bus). Without it
				// the agent's reply lands only in Multica's web UI — the
				// user in WeCom sees no response.
				//
				// WithAttachments adds the second hop: the files the agent
				// bound to that reply are read back out of object storage and
				// sent into the chat behind it. Passed only when this
				// deployment configured storage — with none there is nothing
				// to read an attachment out of, and the option is what the
				// delivery path checks for.
				//
				// DeclareChannelFileDelivery is the same condition said to the
				// agent: a run only gets told it can send a file where this
				// branch actually built the hop that sends it. The two lines
				// sit together on purpose — a deployment that has the storage
				// and a deployment whose agents are promised delivery must be
				// the same deployment, and the only way to keep that true is
				// for one `if` to decide both.
				wecomOutboundOpts := []wecom.OutboundOption{}
				if store != nil {
					wecomOutboundOpts = append(wecomOutboundOpts, wecom.WithAttachments(store))
					h.DeclareChannelFileDelivery(string(wecom.TypeWecom))
				}
				wecom.NewOutbound(queries, wecomSenders, slog.Default(), wecomOutboundOpts...).Register(bus)

				// Ranges the media fetcher may dial despite looking reserved.
				// Empty by default, which leaves the SSRF guard exactly as
				// strict as it ships. A deployment behind a fake-IP proxy
				// needs it: there, every public hostname resolves into the
				// proxy's pool (198.18.0.0/15 is the common one), so WeCom's
				// own COS host is indistinguishable from a metadata endpoint
				// by address alone and every attachment is refused.
				if raw := strings.TrimSpace(os.Getenv("MULTICA_WECOM_MEDIA_ALLOW_CIDRS")); raw != "" {
					for _, err := range wecom.SetMediaAllowedPrefixes(strings.Split(raw, ",")) {
						slog.Error("wecom: ignoring malformed media allow cidr", "error", err)
					}
					slog.Warn("wecom: media guard has an operator allow-list; those ranges are reachable by a URL WeCom supplies",
						"cidrs", raw)
				}

				// Frame tracing: off unless an operator asks for it. It
				// records a bounded prefix of message text, so the fact that
				// it is on has to be visible in the log it is writing into —
				// otherwise a session gets left switched on and nobody
				// notices message content accumulating.
				if wecom.SetTrace(os.Getenv("MULTICA_WECOM_TRACE") == "1") {
					slog.Warn("wecom: frame tracing ON — records message text; unset MULTICA_WECOM_TRACE when done")
				}

				slog.Info("wecom integration enabled (smart bot, long connection)")
				// SINGLE-REPLICA CONSTRAINT: WeCom outbound (agent replies +
				// inbox pushes) is delivered only by the replica holding each
				// bot's in-process WebSocket lease. On a multi-replica
				// deployment, an EventChatDone/EventInboxNew published on another
				// replica cannot reach the lease holder, so those replies are
				// dropped. This is stated conditionally rather than gated on a
				// replica-count signal: the server has no reliable count here,
				// and REDIS_URL means "Redis configured" (it also gates rate
				// limiting), not "more than one replica". See wecom/outbound.go
				// and SELF_HOSTING.md. Remove once outbound routes to the lease
				// holder.
				slog.Warn("wecom integration: WeCom agent replies and inbox pushes are delivered only by the replica holding each bot's WebSocket lease. If you run more than one backend replica, responses produced on a replica that does not hold the lease will be dropped — run the WeCom-enabled backend as a single replica until cross-replica outbound routing is implemented.")
			}
		}
	} else {
		slog.Info("wecom integration disabled (MULTICA_WECOM_SECRET_KEY not set)")
	}

	// Telegram integration. Same shape as Slack: BYO bot token pasted at
	// install, one getUpdates long-polling loop per active installation
	// supervised by the shared engine.Supervisor, resolvers on the generic
	// channel_* tables, outbound streaming via throttled editMessageText on
	// the event bus. Gated by MULTICA_TELEGRAM_SECRET_KEY (the at-rest token
	// encryption key); when unset the handlers return 503 and no Factory is
	// registered.
	if telegramKey, err := secretbox.LoadKey("MULTICA_TELEGRAM_SECRET_KEY"); err == nil {
		box, err := secretbox.New(telegramKey)
		if err != nil {
			slog.Error("telegram: secretbox.New failed; telegram integration disabled", "error", err)
		} else {
			telegramBindingSvc := telegram.NewBindingTokenService(queries, pool)
			h.TelegramBindingTokens = telegramBindingSvc
			telegramReplier := telegram.NewOutboundReplier(telegram.OutboundReplierConfig{
				Binding: telegramBindingSvc,
				Decrypt: box.Open,
				// The bind link (/telegram/bind) is a web-app page: app URL, not
				// the API URL. Mirrors the Slack replier.
				AppURL: appURLFromEnv(),
				Logger: slog.Default(),
			})
			telegramTyping := telegram.NewTypingNotifier(box.Open, "", nil, slog.Default())
			channelRouter.Register(telegram.TypeTelegram, telegram.NewTelegramResolverSet(queries, pool, telegramReplier, telegramTyping))
			telegramOutbound := telegram.NewOutbound(queries, box.Open, "", nil, slog.Default())
			telegramOutbound.Register(bus)
			h.TelegramOutbound = telegramOutbound

			// Per-installation inbound: the Supervisor builds + supervises one
			// long-polling loop per active Telegram installation.
			telegram.RegisterTelegram(channelRegistry, telegram.ChannelDeps{Decrypt: box.Open, Logger: slog.Default()})

			installSvc, ierr := telegram.NewInstallService(queries, pool, box, slog.Default())
			if ierr != nil {
				slog.Error("telegram: InstallService init failed; install disabled", "error", ierr)
			} else {
				h.TelegramInstall = installSvc
			}
			slog.Info("telegram integration enabled (per-installation long polling)")
		}
	} else {
		slog.Info("telegram integration disabled (MULTICA_TELEGRAM_SECRET_KEY not set)")
	}

	// Composio integration (MUL-3720). Gated by COMPOSIO_API_KEY plus the
	// composio_mcp_apps feature flag. The env var is the project-scoped key the
	// standalone SDK authenticates Composio with (sent as x-api-key; the project
	// is resolved from the key, so NO project id is configured). When unset or
	// flag-disabled the whole block is skipped and the composio HTTP handlers
	// return 503; existing deployments are unaffected. An operator opts in by
	// setting COMPOSIO_API_KEY plus a callback base
	// (COMPOSIO_CALLBACK_BASE_URL, falling back to MULTICA_PUBLIC_URL). The
	// toolkit→auth-config mapping is NOT configured here — it is resolved
	// dynamically from the project's /auth_configs at request time, so enabling
	// a toolkit is a dashboard action, not a redeploy. State signing uses
	// COMPOSIO_STATE_SECRET, or a key derived from JWT_SECRET when that is unset.
	if composioAPIKey := strings.TrimSpace(os.Getenv("COMPOSIO_API_KEY")); composioAPIKey != "" {
		if !featureflags.ComposioMCPAppsEnabled(context.Background(), opts.FeatureFlags) {
			slog.Info("composio integration disabled (feature flag off)")
		} else {
			sdkClient, err := composiosdk.NewClient(composiosdk.Options{APIKey: composioAPIKey})
			if err != nil {
				slog.Error("composio: SDK client init failed; composio integration disabled", "error", err)
			} else {
				stateSecret := composioStateSecret()
				callbackBase := composioCallbackBaseURL(signupConfig.PublicURL)
				switch {
				case len(stateSecret) == 0:
					slog.Error("composio: no state secret (set COMPOSIO_STATE_SECRET or JWT_SECRET); composio integration disabled")
				case callbackBase == "":
					slog.Error("composio: no callback base url (set COMPOSIO_CALLBACK_BASE_URL or MULTICA_PUBLIC_URL); composio integration disabled")
				default:
					svc, serr := composiointeg.NewService(sdkClient, queries, composiointeg.Config{
						StateSecret:     stateSecret,
						CallbackBaseURL: callbackBase,
						FrontendBaseURL: appURLFromEnv(),
					})
					if serr != nil {
						slog.Error("composio: service init failed; composio integration disabled", "error", serr)
					} else {
						h.Composio = svc
						// Stage 3 (MUL-3721) hook: feed the per-task MCP
						// overlay builder into TaskService so every Enqueue*
						// path attaches the initiator user's Composio session
						// URL to the task row before the daemon claims it.
						// taskSvc already exists by this point — it was
						// constructed inside NewHandler — and exposes its
						// Composio field for exactly this kind of late wiring,
						// so no Handler-level mutation is needed.
						if h.TaskService != nil {
							h.TaskService.Composio = svc
						}
						slog.Info("composio integration enabled")
					}
				}
			}
		}
	} else {
		slog.Info("composio integration disabled (COMPOSIO_API_KEY not set)")
	}

	// VCS at-rest encryption: the box encrypts per-workspace access tokens and
	// webhook secrets for token-based providers (Forgejo / Gitea / GitLab).
	// Without it, connect/webhook handlers return 503 (so a misconfigured
	// self-host never stores plaintext secrets).
	if vcsKey, err := secretbox.LoadKey("MULTICA_VCS_SECRET_KEY"); err == nil {
		box, err := secretbox.New(vcsKey)
		if err != nil {
			slog.Error("vcs: secretbox.New failed; vcs integration disabled", "error", err)
		} else {
			h.VCSSecretBox = box
			slog.Info("vcs integration enabled")
		}
	} else {
		slog.Info("vcs integration disabled (MULTICA_VCS_SECRET_KEY not set)")
	}

	// Plugin secrets use a dedicated deployment key. Keeping this separate from
	// VCS and channel secrets gives operators an isolated rotation and blast
	// radius; without it, saving a `secret` config field fails closed rather
	// than storing plaintext.
	if pluginKey, err := secretbox.LoadKey("MULTICA_PLUGIN_SECRET_KEY"); err == nil {
		box, err := secretbox.New(pluginKey)
		if err != nil {
			slog.Error("plugins: secretbox.New failed; Plugin secrets disabled", "error", err)
		} else if h.PluginService != nil {
			h.PluginService.Secrets = box
			// The same deployment key, kept raw as well. Sealing and signing
			// need different things from it: a secret config value is sealed
			// and later opened, while a hook signature must be REPRODUCED on
			// demand, which a box cannot do. Each installation's signing secret
			// is derived from this rather than stored, so no row holds a usable
			// one.
			h.PluginService.DeploymentKey = pluginKey
			slog.Info("Plugin secret encryption enabled")
		}
	} else {
		slog.Info("Plugin secrets disabled (MULTICA_PLUGIN_SECRET_KEY not set)")
	}

	// Hook engine. Event-triggered hooks are dispatched off the bus onto a
	// worker pool: Bus.Publish runs listeners inline on the publishing request's
	// goroutine, so anything that dials a third-party endpoint from there would
	// put an outside server on the critical path of creating an issue.
	if h.PluginService != nil {
		h.PluginService.Callbacks = service.NewCallbackTokens()
		// Omitted rather than sent relative: a handler receiving
		// "/api/v1/plugin" cannot call anything with it, and a broken absolute
		// URL is harder to diagnose than an absent one.
		if publicURL := strings.TrimSpace(os.Getenv("MULTICA_PUBLIC_URL")); publicURL != "" {
			h.PluginService.CallbackBaseURL = strings.TrimSuffix(publicURL, "/") + "/api/v1/plugin"
		} else {
			slog.Warn("plugins: MULTICA_PUBLIC_URL is not set; hook callbacks will carry no callback_url")
		}
		// The flag reaches the event path only through the service: a worker has
		// no request to read it from.
		h.PluginService.FeatureFlags = h.FeatureFlags
		pluginEvents := service.NewPluginEventDispatcher(h.PluginService)
		service.SubscribePluginEvents(bus, pluginEvents)
	}

	if opts.HeartbeatScheduler != nil {
		h.HeartbeatScheduler = opts.HeartbeatScheduler
	}
	// Auth caches: PAT cache is shared between the regular Auth middleware,
	// the DaemonAuth fallback (mul_) path, and the revoke handler
	// (invalidate). DaemonTokenCache backs the DaemonAuth mdt_ path. Both
	// constructors return nil when rdb is nil — every consumer handles that
	// as "no cache, always hit DB".
	patCache := auth.NewPATCache(rdb)
	daemonTokenCache := auth.NewDaemonTokenCache(rdb)
	h.PATCache = patCache
	h.DaemonTokenCache = daemonTokenCache
	h.MembershipCache = auth.NewMembershipCache(rdb)

	// Cloud PAT verifier: validates mcn_ tokens against Multica Cloud
	// Fleet. Returns nil when no Fleet URL is configured — the Auth /
	// DaemonAuth middlewares treat nil as "mcn_ not supported" and
	// reject with 401, instead of falling through to mul_/JWT paths.
	// Reuses MULTICA_CLOUD_FLEET_URL (the same URL the cloud-runtime
	// proxy uses) so a deployment doesn't need a second config knob.
	cloudPATVerifier := auth.NewCloudPATVerifier(auth.CloudPATVerifierConfig{
		FleetBaseURL: signupConfig.CloudRuntimeFleetURL,
		Redis:        rdb,
	})

	// Empty-claim cache: lets the daemon poll path skip a Postgres
	// scan when a recent check confirmed the runtime had no queued
	// task. Returns nil when rdb is nil — TaskService treats that
	// as "no cache, always hit DB" (existing behavior).
	h.TaskService.EmptyClaim = service.NewEmptyClaimCache(rdb)

	// Wire WS heartbeat after stores are finalized so the WS path uses the
	// same (possibly Redis-backed) stores as the HTTP path.
	daemonHub.SetHeartbeatHandler(h.HandleDaemonWSHeartbeat)
	// WS-first claim (MUL-4257): route daemon:rpc_request frames (e.g.
	// tasks.claim) through the same handlers as the HTTP endpoints.
	daemonHub.SetRPCHandler(h.DaemonRPCHandler)
	return patCache, daemonTokenCache, cloudPATVerifier
}
