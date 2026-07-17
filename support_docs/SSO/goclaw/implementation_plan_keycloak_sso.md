# Implementation Plan: Keycloak SSO Integration via OpenID Connect

**Date:** 2026-06-30
**Objective:** Integrate Keycloak as an SSO identity provider using OpenID Connect (OIDC) protocol. Users must authenticate through Keycloak before accessing GoClaw. After successful Keycloak authentication, the user's email is mapped to a pre-provisioned GoClaw token via an external database (`user_mapping_goclaw` table in Talita DB). If mapping succeeds, the user enters GoClaw with the mapped token. If mapping fails, the user sees an "Unauthorized, please contact Admin IT" page.

---

## Architecture Overview

### Current Auth System

GoClaw currently uses **token-based authentication** with three mechanisms:
1. **Gateway Token** — static secret in `.env.local` (`GOCLAW_GATEWAY_TOKEN`), validated via constant-time comparison
2. **API Keys** — `goclaw_<32hex>` format, stored hashed in DB, resolved via cache
3. **Browser Pairing** — device code flow for browser clients

The web UI (`ui/web/`) has a `/login` page where users manually enter a token + user ID. The `RequireAuth` component (`require-auth.tsx:16-17`) redirects unauthenticated users to `/login`. After login, credentials are stored in Zustand (`use-auth-store.ts`) and persisted to `localStorage`. The WebSocket `connect` method (`router.go:125-318`) authenticates using the token.

**Key insight:** The SSO integration does NOT replace the existing token-based auth. Instead, it provides a **new way to obtain** a pre-provisioned GoClaw token — via Keycloak authentication + external DB mapping. Once the token is obtained, the existing auth flow (WS connect, HTTP Bearer, etc.) works unchanged.

### Proposed SSO Flow

```
┌─────────┐     ┌───────────┐     ┌──────────┐     ┌────────────┐     ┌──────────┐
│  User   │────▶│  GoClaw   │────▶│ Keycloak │────▶│  GoClaw    │────▶│ Talita   │
│ accesses│     │  backend  │     │  login   │     │  callback  │     │ DB       │
│ GoClaw  │     │ redirect  │     │ page     │     │ exchange   │     │ mapping  │
└─────────┘     └───────────┘     └──────────┘     └────────────┘     └──────────┘
                                                        │
                                               ┌────────┴────────┐
                                               │                 │
                                          Mapping found     Mapping not found
                                               │                 │
                                               ▼                 ▼
                                          Set cookie        Redirect to
                                          + redirect        /unauthorized
                                          to /sso/callback
                                               │
                                               ▼
                                        Frontend reads cookie
                                        → calls /v1/auth/sso/exchange
                                        → gets {token, userId}
                                        → stores in Zustand
                                        → navigates to app
```

### Detailed Flow Steps

1. **User accesses GoClaw** (`/` or any protected route)
2. **Frontend `RequireAuth`** checks auth store — no token found
3. **Frontend redirects** to `/auth/keycloak/login` (backend endpoint)
4. **Backend** generates PKCE verifier + state, stores in short-lived cookie, redirects to Keycloak authorization endpoint
5. **User inputs** username + password on Keycloak login page
6. **Keycloak** validates credentials, redirects to `/auth/keycloak/callback` with authorization code + state
7. **Backend callback handler:**
   - Validates state (CSRF protection)
   - Exchanges authorization code for ID token + access token (PKCE verified)
   - Extracts `email` from ID token claims (or userinfo endpoint fallback)
   - Queries Talita DB: `SELECT username, token FROM user_mapping_goclaw WHERE email = $1`
   - **If found:** Sets a signed, HttpOnly cookie with `{token, userId, exp}`, redirects to `/sso/callback` (frontend route)
   - **If not found:** Redirects to `/unauthorized` (frontend route)
8. **Frontend `/sso/callback`:**
   - Calls `POST /v1/auth/sso/exchange` (cookie sent automatically by browser)
   - Backend verifies cookie signature, returns `{ token, user_id }` as JSON, clears cookie
   - Frontend calls `setCredentials(token, userId)` in auth store
   - Navigates to the originally requested page (or `/overview`)
9. **Subsequent requests** use the GoClaw token as before (WS connect, HTTP Bearer header)

---

## Configuration & Environment Variables

### New Environment Variables

Add to `.env.local` (or system environment):

```bash
# ─── Keycloak SSO Configuration ───
GOCLAW_SSO_ENABLED=true                                                          # Master toggle for SSO
GOCLAW_SSO_KEYCLOAK_ISSUER=https://larasati.lintasarta.co.id/realms/dev         # Keycloak OIDC Issuer URL (base + realm)
GOCLAW_SSO_CLIENT_ID=la-claw                                                     # OIDC client ID
GOCLAW_SSO_CLIENT_SECRET=<redacted>                                              # OIDC client secret (from .env)
GOCLAW_SSO_REDIRECT_URL=https://goclaw.example.com/auth/keycloak/callback        # OIDC redirect URI

# ─── External Mapping Database (Talita) ───
TALITA_DB_POSTGRES_HOST=10.24.117.58
TALITA_DB_POSTGRES_USER=talita
TALITA_DB_POSTGRES_PASSWORD=P4ssword
TALITA_DB_POSTGRES_PORT=5432
TALITA_DB_POSTGRES_DATABASE=talita_db
```

> **Note on Issuer URL:** The `GOCLAW_SSO_KEYCLOAK_ISSUER` env var contains the full OIDC issuer URL as defined by Keycloak: `{base_url}/realms/{realm}`. For this deployment, the issuer is `https://larasati.lintasarta.co.id/realms/dev`, where:
> - **Base URL:** `https://larasati.lintasarta.co.id`
> - **Realm:** `dev`
>
> The `go-oidc` library uses this issuer URL directly for OIDC discovery by appending `/.well-known/openid-configuration` (i.e., `https://larasati.lintasarta.co.id/realms/dev/.well-known/openid-configuration`). This is the standard OIDC discovery mechanism — no separate base URL + realm config needed.

### New Config Struct

**File:** `internal/config/config_sso.go` (NEW)

```go
package config

// SSOConfig holds Keycloak OIDC and external user-mapping configuration.
// All fields are loaded from environment variables only (json:"-"), 
// consistent with DatabaseConfig pattern (config.go:89-96).
type SSOConfig struct {
    Enabled        bool   `json:"-"`
    KeycloakIssuer string `json:"-"`  // e.g. "https://larasati.lintasarta.co.id/realms/dev"
    ClientID       string `json:"-"`  // e.g. "la-claw"
    ClientSecret   string `json:"-"`  // from env, never in config.json
    RedirectURL    string `json:"-"`  // e.g. "https://goclaw.example.com/auth/keycloak/callback"

    // External mapping database (Talita)
    MappingDBHost     string `json:"-"`
    MappingDBPort     string `json:"-"`
    MappingDBUser     string `json:"-"`
    MappingDBPassword string `json:"-"`
    MappingDBName     string `json:"-"`
}
```

### Env Overlay Integration

**File:** `internal/config/config_load.go` (MODIFY — `applyEnvOverrides()` method, ~line 86-278)

Add SSO env var reading in `applyEnvOverrides()`:

```go
// SSO / Keycloak
c.SSO.Enabled = envBool("GOCLAW_SSO_ENABLED", c.SSO.Enabled)
c.SSO.KeycloakIssuer = envStr("GOCLAW_SSO_KEYCLOAK_ISSUER", c.SSO.KeycloakIssuer)
c.SSO.ClientID = envStr("GOCLAW_SSO_CLIENT_ID", c.SSO.ClientID)
c.SSO.ClientSecret = envStr("GOCLAW_SSO_CLIENT_SECRET", c.SSO.ClientSecret)
c.SSO.RedirectURL = envStr("GOCLAW_SSO_REDIRECT_URL", c.SSO.RedirectURL)

// External mapping DB (Talita)
c.SSO.MappingDBHost = envStr("TALITA_DB_POSTGRES_HOST", c.SSO.MappingDBHost)
c.SSO.MappingDBPort = envStr("TALITA_DB_POSTGRES_PORT", c.SSO.MappingDBPort)
c.SSO.MappingDBUser = envStr("TALITA_DB_POSTGRES_USER", c.SSO.MappingDBUser)
c.SSO.MappingDBPassword = envStr("TALITA_DB_POSTGRES_PASSWORD", c.SSO.MappingDBPassword)
c.SSO.MappingDBName = envStr("TALITA_DB_POSTGRES_DATABASE", c.SSO.MappingDBName)
```

### Config Struct Addition

**File:** `internal/config/config.go` (MODIFY — line 42-60)

Add `SSO SSOConfig` field to the main `Config` struct:

```go
type Config struct {
    DataDir   string          `json:"data_dir,omitempty"`
    Agents    AgentsConfig    `json:"agents"`
    Channels  ChannelsConfig  `json:"channels"`
    Providers ProvidersConfig `json:"providers"`
    Gateway   GatewayConfig   `json:"gateway"`
    Tools     ToolsConfig     `json:"tools"`
    Sessions  SessionsConfig  `json:"sessions"`
    Database  DatabaseConfig  `json:"database"`
    Tts       TtsConfig       `json:"tts"`
    Audio     *AudioConfig    `json:"audio,omitempty"`
    Cron      CronConfig      `json:"cron"`
    Telemetry TelemetryConfig `json:"telemetry"`
    Tailscale TailscaleConfig `json:"tailscale"`
    Bindings  []AgentBinding  `json:"bindings,omitempty"`
    Hooks     HooksConfig     `json:"hooks"`
    SSO       SSOConfig       `json:"-"`   // NEW — env-only, never serialized
    mu        sync.RWMutex
}
```

---

## Backend Changes

### New Package: `internal/sso/` — Keycloak OIDC Client

#### File: `internal/sso/oidc.go` (NEW)

Handles OIDC protocol: discovery, authorization URL generation, token exchange, user info retrieval.

**Dependencies to add to `go.mod`:**
- `github.com/coreos/go-oidc/v3/oidc` — OIDC client library (standard Go OIDC implementation)
- `golang.org/x/oauth2` — already an indirect dependency (`go.mod:157`), promote to direct

**Key types and functions:**

```go
package sso

import (
    "context"
    "crypto/rand"
    "encoding/base64"
    "errors"
    "fmt"

    "github.com/coreos/go-oidc/v3/oidc"
    "golang.org/x/oauth2"
)

// OIDCClient wraps OIDC provider + OAuth2 config for Keycloak.
type OIDCClient struct {
    provider *oidc.Provider
    oauth2   *oauth2.Config
    verifier *oidc.IDTokenVerifier
}

// NewOIDCClient discovers the Keycloak OIDC endpoints via the issuer's well-known URL.
// The issuer URL is the full Keycloak realm URL, e.g. "https://larasati.lintasarta.co.id/realms/dev"
// Discovery URL: {issuer}/.well-known/openid-configuration
func NewOIDCClient(ctx context.Context, issuer, clientID, clientSecret, redirectURL string) (*OIDCClient, error)

// AuthURL generates the Keycloak authorization URL with PKCE + state.
// Returns: authURL, codeVerifier, state (codeVerifier + state stored by caller in cookie/session)
func (c *OIDCClient) AuthURL(state, codeVerifier string) string

// ExchangeCode exchanges the authorization code for tokens.
// Verifies PKCE codeVerifier against the code_challenge sent in AuthURL.
// Returns the ID token's email claim.
func (c *OIDCClient) ExchangeCode(ctx context.Context, code, codeVerifier string) (email string, err error)

// GenerateCodeVerifier generates a PKCE code_verifier (43-128 chars, base64url).
func GenerateCodeVerifier() (string, error)

// GenerateState generates a random state parameter for CSRF protection.
func GenerateState() (string, error)
```

**Design notes:**
- Uses OIDC discovery via the issuer URL — the `go-oidc` library calls `{issuer}/.well-known/openid-configuration` to auto-resolve all Keycloak endpoints (authorization, token, userinfo, JWKS). For this deployment: `https://larasati.lintasarta.co.id/realms/dev/.well-known/openid-configuration`
- PKCE (S256) is mandatory for security, even though `client_secret` is used (defense-in-depth)
- `state` parameter prevents CSRF — stored in a short-lived cookie and verified on callback
- Email is extracted from the ID token's `email` claim. Fallback to userinfo endpoint if claim is missing (some Keycloak configs don't include email in ID token by default)

#### File: `internal/sso/mapping.go` (NEW)

Handles the external database connection to Talita and user mapping lookup.

```go
package sso

import (
    "context"
    "database/sql"
    "fmt"
    "time"

    _ "github.com/jackc/pgx/v5/stdlib" // same driver as main GoClaw DB
)

// MappingDB connects to the external Talita database for user mapping.
// Uses a SEPARATE connection pool — does NOT share with the main GoClaw DB.
type MappingDB struct {
    db *sql.DB
}

// NewMappingDB creates a new connection pool to the Talita database.
// DSN pattern: postgres://{user}:{password}@{host}:{port}/{database}?sslmode=disable
func NewMappingDB(host, port, user, password, dbname string) (*MappingDB, error)

// UserMapping represents a row in user_mapping_goclaw.
type UserMapping struct {
    Email    string `json:"email"`
    Username string `json:"username"`
    Token    string `json:"token"`
}

// LookupByEmail queries user_mapping_goclaw for a user by email.
// Returns ErrMappingNotFound if no row matches.
func (m *MappingDB) LookupByEmail(ctx context.Context, email string) (*UserMapping, error)

// Close closes the database connection pool.
func (m *MappingDB) Close() error

var ErrMappingNotFound = errors.New("user mapping not found")
```

**SQL query:**
```sql
SELECT email, username, token FROM user_mapping_goclaw WHERE email = $1
```

**Design notes:**
- Uses `pgx/v5/stdlib` driver (same as main GoClaw DB, already in `go.mod`)
- Separate `sql.DB` pool — completely isolated from main GoClaw DB
- Connection pool settings: `SetMaxOpenConns(5)`, `SetMaxIdleConns(2)`, `SetConnMaxLifetime(30*time.Minute)` — low volume (only queried during SSO login)
- `sslmode=disable` for internal network (10.24.117.58). Can be changed to `sslmode=require` if needed
- Parameterized query (`$1`) — prevents SQL injection
- Context-aware (respects request cancellation/timeout)

#### File: `internal/sso/cookie.go` (NEW)

Handles the signed SSO session cookie used to pass the mapped token from backend callback to frontend.

```go
package sso

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
    "net/http"
    "time"
)

// CookieName is the name of the SSO session cookie.
const CookieName = "goclaw_sso_session"

// CookieTTL is the maximum lifetime of the SSO cookie (short-lived, single-use).
const CookieTTL = 30 * time.Second

// SessionPayload is the data encoded in the SSO cookie.
type SessionPayload struct {
    Token  string `json:"token"`
    UserID string `json:"user_id"`
    Exp    int64  `json:"exp"` // unix timestamp
}

// SetSessionCookie sets a signed, HttpOnly cookie with the session payload.
// The cookie is signed with HMAC-SHA256 using the gateway token as the key.
func SetSessionCookie(w http.ResponseWriter, payload SessionPayload, signingKey string) error

// ReadSessionCookie reads and verifies the SSO cookie, returning the payload.
// Returns error if cookie is missing, expired, or signature is invalid.
func ReadSessionCookie(r *http.Request, signingKey string) (*SessionPayload, error)

// ClearSessionCookie deletes the SSO cookie.
func ClearSessionCookie(w http.ResponseWriter)
```

**Design notes:**
- Cookie is `HttpOnly` (not accessible via JavaScript — prevents XSS token theft)
- Cookie is `Secure` (only sent over HTTPS — set to `false` only in dev mode)
- Cookie is `SameSite=Lax` (allows redirect from Keycloak back to GoClaw)
- Cookie is signed with HMAC-SHA256 using the gateway token as signing key — prevents tampering
- Cookie TTL is 30 seconds — just enough time for the frontend to call the exchange endpoint
- Cookie is single-use — the exchange endpoint clears it after reading

### New HTTP Handler: `internal/http/sso_handler.go` (NEW)

Registers SSO routes on the gateway mux.

```go
package http

import (
    "context"
    "log/slog"
    "net/http"

    "github.com/nextlevelbuilder/goclaw/internal/sso"
)

// SSOHandler handles Keycloak OIDC login, callback, and token exchange.
type SSOHandler struct {
    oidcClient   *sso.OIDCClient
    mappingDB    *sso.MappingDB
    gatewayToken string // used as cookie signing key
    redirectURL  string // frontend URL to redirect after successful SSO
    enabled      bool
}

func NewSSOHandler(oidcClient *sso.OIDCClient, mappingDB *sso.MappingDB, gatewayToken, redirectURL string, enabled bool) *SSOHandler

// RegisterRoutes registers SSO routes on the mux.
// These routes are PUBLIC (no requireAuth) — they handle the auth flow itself.
func (h *SSOHandler) RegisterRoutes(mux *http.ServeMux) {
    mux.HandleFunc("GET /auth/keycloak/login", h.handleLogin)
    mux.HandleFunc("GET /auth/keycloak/callback", h.handleCallback)
    mux.HandleFunc("POST /v1/auth/sso/exchange", h.handleExchange)
}
```

#### Route 1: `GET /auth/keycloak/login`

Initiates the OIDC authorization code flow.

```go
func (h *SSOHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
    // 1. Generate PKCE code_verifier + state
    codeVerifier, _ := sso.GenerateCodeVerifier()
    state, _ := sso.GenerateState()

    // 2. Store code_verifier + state in a short-lived cookie (HttpOnly, 5 min TTL)
    //    Cookie name: "goclaw_sso_state"
    //    Payload: {state, code_verifier} signed with HMAC
    setStateCookie(w, state, codeVerifier, h.gatewayToken)

    // 3. Generate Keycloak authorization URL
    authURL := h.oidcClient.AuthURL(state, codeVerifier)

    // 4. Redirect (302) to Keycloak
    http.Redirect(w, r, authURL, http.StatusFound)
}
```

#### Route 2: `GET /auth/keycloak/callback`

Handles the OIDC callback from Keycloak.

```go
func (h *SSOHandler) handleCallback(w http.ResponseWriter, r *http.Request) {
    // 1. Read state cookie, verify state parameter matches (CSRF protection)
    stateCookie, err := readStateCookie(r, h.gatewayToken)
    if err != nil || stateCookie.State != r.URL.Query().Get("state") {
        slog.Warn("security.sso_state_mismatch")
        http.Redirect(w, r, "/unauthorized", http.StatusFound)
        return
    }

    // 2. Get authorization code from query params
    code := r.URL.Query().Get("code")
    if code == "" {
        http.Redirect(w, r, "/unauthorized", http.StatusFound)
        return
    }

    // 3. Exchange code for ID token, extract email (PKCE verified)
    email, err := h.oidcClient.ExchangeCode(r.Context(), code, stateCookie.CodeVerifier)
    if err != nil {
        slog.Error("sso.token_exchange", "error", err)
        http.Redirect(w, r, "/unauthorized", http.StatusFound)
        return
    }

    // 4. Query Talita DB for user mapping
    mapping, err := h.mappingDB.LookupByEmail(r.Context(), email)
    if err != nil {
        slog.Warn("sso.mapping_not_found", "email", email)
        http.Redirect(w, r, "/unauthorized", http.StatusFound)
        return
    }

    // 5. Set signed session cookie with mapped token + username
    payload := sso.SessionPayload{
        Token:  mapping.Token,
        UserID: mapping.Username,
        Exp:    time.Now().Add(sso.CookieTTL).Unix(),
    }
    sso.SetSessionCookie(w, payload, h.gatewayToken)

    // 6. Clear state cookie
    clearStateCookie(w)

    // 7. Redirect to frontend SSO callback route
    http.Redirect(w, r, "/sso/callback", http.StatusFound)
}
```

#### Route 3: `POST /v1/auth/sso/exchange`

Exchanges the SSO session cookie for GoClaw credentials. Called by the frontend.

```go
func (h *SSOHandler) handleExchange(w http.ResponseWriter, r *http.Request) {
    // 1. Read + verify SSO session cookie
    payload, err := sso.ReadSessionCookie(r, h.gatewayToken)
    if err != nil {
        writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired SSO session"})
        return
    }

    // 2. Clear the cookie (single-use)
    sso.ClearSessionCookie(w)

    // 3. Return GoClaw credentials
    writeJSON(w, http.StatusOK, map[string]string{
        "token":   payload.Token,
        "user_id": payload.UserID,
    })
}
```

### Gateway Server Integration

**File:** `internal/gateway/server.go` (MODIFY — `BuildMux()` function, ~line 134-208)

Add SSO handler registration before the web UI catch-all:

```go
// SSO handler (Keycloak OIDC) — registered before web UI catch-all
if s.ssoHandler != nil {
    s.ssoHandler.RegisterRoutes(mux)
}
```

**File:** `internal/gateway/server.go` (MODIFY — `Server` struct)

Add `ssoHandler` field to the Server struct:

```go
type Server struct {
    // ... existing fields ...
    ssoHandler *httpapi.SSOHandler  // NEW
}

func (s *Server) SetSSOHandler(h *httpapi.SSOHandler) {
    s.ssoHandler = h
}
```

### Gateway Startup Wiring

**File:** `cmd/gateway.go` (MODIFY — startup sequence, after store setup ~line 145, before server creation ~line 283)

Add SSO initialization:

```go
// SSO / Keycloak setup (optional, env-gated)
var ssoHandler *httpapi.SSOHandler
if cfg.SSO.Enabled {
    // 1. Initialize OIDC client (discovers Keycloak endpoints via issuer URL)
    oidcClient, err := sso.NewOIDCClient(ctx,
        cfg.SSO.KeycloakIssuer,
        cfg.SSO.ClientID,
        cfg.SSO.ClientSecret,
        cfg.SSO.RedirectURL,
    )
    if err != nil {
        slog.Error("sso.oidc_init", "error", err)
        os.Exit(1)
    }

    // 2. Initialize external mapping DB (Talita)
    mappingDB, err := sso.NewMappingDB(
        cfg.SSO.MappingDBHost,
        cfg.SSO.MappingDBPort,
        cfg.SSO.MappingDBUser,
        cfg.SSO.MappingDBPassword,
        cfg.SSO.MappingDBName,
    )
    if err != nil {
        slog.Error("sso.mapping_db_init", "error", err)
        os.Exit(1)
    }
    defer mappingDB.Close()

    // 3. Create SSO handler
    ssoHandler = httpapi.NewSSOHandler(oidcClient, mappingDB, cfg.Gateway.Token, cfg.SSO.RedirectURL, true)
    slog.Info("sso: Keycloak OIDC enabled", "issuer", cfg.SSO.KeycloakIssuer)
}

// ... later, after server creation ...
if ssoHandler != nil {
    server.SetSSOHandler(ssoHandler)
}
```

### Web UI Handler Update

**File:** `internal/webui/handler.go` (MODIFY — line 11)

Add SSO auth routes to the `apiPrefixes` list so the SPA handler doesn't intercept them:

```go
var apiPrefixes = []string{"/v1/", "/ws", "/health", "/mcp/", "/auth/"}
```

This ensures `/auth/keycloak/login` and `/auth/keycloak/callback` are handled by the backend, not the SPA fallback.

---

## Frontend Changes (Web UI)

### New Route: `/sso/callback`

**File:** `ui/web/src/pages/sso/sso-callback-page.tsx` (NEW)

A loading page that exchanges the SSO cookie for GoClaw credentials.

```tsx
import { useEffect, useRef } from "react";
import { useNavigate, useLocation } from "react-router";
import { useAuthStore } from "@/stores/use-auth-store";
import { ROUTES } from "@/lib/constants";

export function SsoCallbackPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const setCredentials = useAuthStore((s) => s.setCredentials);
  const calledRef = useRef(false); // prevent double-call in StrictMode

  useEffect(() => {
    if (calledRef.current) return;
    calledRef.current = true;

    async function exchange() {
      try {
        const res = await fetch("/v1/auth/sso/exchange", {
          method: "POST",
          credentials: "include", // send the SSO cookie
          headers: { "Content-Type": "application/json" },
        });

        if (!res.ok) {
          navigate("/unauthorized", { replace: true });
          return;
        }

        const data = await res.json();
        // Store credentials in auth store (same as manual token login)
        setCredentials(data.token, data.user_id);

        // Navigate to the originally requested page, or overview
        const from = (location.state as any)?.from?.pathname ?? ROUTES.OVERVIEW;
        navigate(from, { replace: true });
      } catch {
        navigate("/unauthorized", { replace: true });
      }
    }

    exchange();
  }, []);

  return (
    <div className="flex h-dvh items-center justify-center">
      <div className="flex flex-col items-center gap-3">
        <img src="/goclaw-icon.svg" alt="" className="h-10 w-10 animate-pulse opacity-50" />
        <p className="text-sm text-muted-foreground">Authenticating...</p>
      </div>
    </div>
  );
}
```

**Design notes:**
- Uses `useRef` to prevent double API call in React StrictMode (dev mode double-renders effects)
- Uses `credentials: "include"` to send the HttpOnly cookie
- Uses `h-dvh` for mobile viewport height (CLAUDE.md mobile rule)
- On failure, redirects to `/unauthorized`

### New Route: `/unauthorized`

**File:** `ui/web/src/pages/sso/unauthorized-page.tsx` (NEW)

The "Unauthorized, please contact Admin IT" page.

```tsx
import { useTranslation } from "react-i18next";
import { ShieldAlert } from "lucide-react";

export function UnauthorizedPage() {
  const { t } = useTranslation("sso");

  return (
    <div className="flex h-dvh items-center justify-center bg-background">
      <div className="flex flex-col items-center gap-4 px-6 text-center">
        <ShieldAlert className="h-16 w-16 text-destructive" />
        <h1 className="text-2xl font-semibold tracking-tight">
          {t("unauthorized.title")}
        </h1>
        <p className="text-base text-muted-foreground max-w-md">
          {t("unauthorized.message")}
        </p>
      </div>
    </div>
  );
}
```

**Content:** "Unauthorized, please contact Admin IT" (as specified by user)

### Route Registration

**File:** `ui/web/src/lib/routes.ts` (MODIFY)

Add new route constants:

```typescript
export const ROUTES = {
  LOGIN: "/login",
  SSO_CALLBACK: "/sso/callback",       // NEW
  UNAUTHORIZED: "/unauthorized",       // NEW
  // ... rest unchanged ...
} as const;
```

**File:** `ui/web/src/routes.tsx` (MODIFY — line 136-137)

Add new routes as public (no `RequireAuth`):

```tsx
// Lazy-loaded SSO pages
const SsoCallbackPage = lazyWithRetry(() =>
  import("@/pages/sso/sso-callback-page").then((m) => ({ default: m.SsoCallbackPage })),
);
const UnauthorizedPage = lazyWithRetry(() =>
  import("@/pages/sso/unauthorized-page").then((m) => ({ default: m.UnauthorizedPage })),
);

// In <Routes>:
<Route path={ROUTES.SSO_CALLBACK} element={<SsoCallbackPage />} />
<Route path={ROUTES.UNAUTHORIZED} element={<UnauthorizedPage />} />
```

These routes are **public** (no `RequireAuth` wrapper) — they handle the auth flow itself.

### Modify `RequireAuth` for SSO Redirect

**File:** `ui/web/src/components/shared/require-auth.tsx` (MODIFY — line 16-17)

When SSO is enabled, redirect to Keycloak login instead of the manual `/login` page:

```tsx
export function RequireAuth({ children }: { children: React.ReactNode }) {
  const token = useAuthStore((s) => s.token);
  const userId = useAuthStore((s) => s.userId);
  const senderID = useAuthStore((s) => s.senderID);
  const connected = useAuthStore((s) => s.connected);
  const tenantSelected = useAuthStore((s) => s.tenantSelected);
  const availableTenants = useAuthStore((s) => s.availableTenants);
  const isOwner = useAuthStore((s) => s.isOwner);
  const location = useLocation();

  // Not authenticated
  if ((!token && !senderID) || !userId) {
    // Check if SSO is enabled via a runtime flag
    const ssoEnabled = useUiStore.getState().ssoEnabled;
    if (ssoEnabled) {
      // Redirect to backend Keycloak login endpoint
      // Preserve the intended destination for post-SSO redirect
      const returnUrl = encodeURIComponent(location.pathname + location.search);
      window.location.href = `/auth/keycloak/login?return_to=${returnUrl}`;
      return null;
    }
    return <Navigate to={ROUTES.LOGIN} state={{ from: location }} replace />;
  }

  // ... rest unchanged ...
}
```

**Note:** `window.location.href` is used (not React Router `Navigate`) because `/auth/keycloak/login` is a backend endpoint that performs an HTTP 302 redirect to Keycloak — it's not a client-side route.

### SSO Enabled Flag

**File:** `ui/web/src/stores/use-ui-store.ts` (MODIFY)

Add `ssoEnabled` flag to the UI store:

```typescript
interface UIState {
  // ... existing fields ...
  ssoEnabled: boolean;
  setSsoEnabled: (enabled: boolean) => void;
}
```

**How the frontend learns if SSO is enabled:**

Add a new public endpoint `GET /v1/auth/sso/status` that returns `{ "enabled": true/false }`. The frontend calls this on app startup.

**File:** `internal/http/sso_handler.go` (MODIFY — add to `RegisterRoutes`)

```go
mux.HandleFunc("GET /v1/auth/sso/status", h.handleStatus)

func (h *SSOHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, http.StatusOK, map[string]bool{"enabled": h.enabled})
}
```

**File:** `ui/web/src/App.tsx` (MODIFY)

Add a startup effect that checks SSO status:

```tsx
useEffect(() => {
  fetch("/v1/auth/sso/status")
    .then((res) => res.json())
    .then((data) => {
      useUiStore.getState().setSsoEnabled(data.enabled);
    })
    .catch(() => {
      useUiStore.getState().setSsoEnabled(false);
    });
}, []);
```

### Login Page SSO Awareness

**File:** `ui/web/src/pages/login/login-page.tsx` (MODIFY)

When SSO is enabled, show a "Login with Keycloak" button instead of (or alongside) the manual token form:

```tsx
const ssoEnabled = useUiStore((s) => s.ssoEnabled);

if (ssoEnabled) {
  // Show "Login with Keycloak" button that redirects to /auth/keycloak/login
  // Optionally keep manual token form behind a "Advanced" toggle for admin fallback
  return (
    <div className="flex h-dvh items-center justify-center">
      <div className="flex flex-col items-center gap-6">
        <img src="/goclaw-icon.svg" alt="GoClaw" className="h-12 w-12" />
        <Button onClick={() => window.location.href = "/auth/keycloak/login"}>
          {t("login.sso.button")}
        </Button>
      </div>
    </div>
  );
}
// ... existing token/pairing forms as fallback ...
```

### i18n Keys

**New namespace:** `sso`

#### `ui/web/src/i18n/locales/en/sso.json` (NEW)
```json
{
  "unauthorized": {
    "title": "Unauthorized",
    "message": "Unauthorized, please contact Admin IT"
  },
  "callback": {
    "authenticating": "Authenticating..."
  },
  "login": {
    "ssoButton": "Login with Keycloak"
  }
}
```

#### `ui/web/src/i18n/locales/vi/sso.json` (NEW)
```json
{
  "unauthorized": {
    "title": "Không có quyền truy cập",
    "message": "Không có quyền truy cập, vui lòng liên hệ Admin IT"
  },
  "callback": {
    "authenticating": "Đang xác thực..."
  },
  "login": {
    "ssoButton": "Đăng nhập với Keycloak"
  }
}
```

#### `ui/web/src/i18n/locales/zh/sso.json` (NEW)
```json
{
  "unauthorized": {
    "title": "未授权",
    "message": "未授权，请联系 IT 管理员"
  },
  "callback": {
    "authenticating": "正在验证..."
  },
  "login": {
    "ssoButton": "使用 Keycloak 登录"
  }
}
```

**File:** `ui/web/src/i18n/index.ts` (MODIFY — add `sso` namespace to i18next config)

---

## Changes Overview

| # | File | Type | Description |
|---|------|------|-------------|
| **Backend — Config** | | | |
| 1 | `internal/config/config_sso.go` | **NEW** | SSOConfig struct (env-only fields) |
| 2 | `internal/config/config.go` | **MODIFY** | Add `SSO SSOConfig` field to Config struct |
| 3 | `internal/config/config_load.go` | **MODIFY** | Add SSO env var reading in `applyEnvOverrides()` |
| **Backend — SSO Package** | | | |
| 4 | `internal/sso/oidc.go` | **NEW** | OIDC client: discovery, auth URL, token exchange, email extraction |
| 5 | `internal/sso/mapping.go` | **NEW** | External DB connection (Talita), user mapping lookup |
| 6 | `internal/sso/cookie.go` | **NEW** | Signed SSO session cookie (HMAC-SHA256) |
| **Backend — HTTP Handler** | | | |
| 7 | `internal/http/sso_handler.go` | **NEW** | SSO HTTP handler: login, callback, exchange, status routes |
| **Backend — Gateway Integration** | | | |
| 8 | `internal/gateway/server.go` | **MODIFY** | Add `ssoHandler` field, `SetSSOHandler()`, register routes in `BuildMux()` |
| 9 | `internal/webui/handler.go` | **MODIFY** | Add `/auth/` to `apiPrefixes` |
| 10 | `cmd/gateway.go` | **MODIFY** | SSO initialization + wiring at startup |
| **Backend — Dependencies** | | | |
| 11 | `go.mod` / `go.sum` | **MODIFY** | Add `github.com/coreos/go-oidc/v3`, promote `golang.org/x/oauth2` to direct |
| **Frontend — New Pages** | | | |
| 12 | `ui/web/src/pages/sso/sso-callback-page.tsx` | **NEW** | SSO callback: exchange cookie → store credentials → navigate |
| 13 | `ui/web/src/pages/sso/unauthorized-page.tsx` | **NEW** | "Unauthorized, please contact Admin IT" page |
| **Frontend — Routing** | | | |
| 14 | `ui/web/src/lib/routes.ts` | **MODIFY** | Add `SSO_CALLBACK` and `UNAUTHORIZED` route constants |
| 15 | `ui/web/src/routes.tsx` | **MODIFY** | Register `/sso/callback` and `/unauthorized` routes (public) |
| **Frontend — Auth Flow** | | | |
| 16 | `ui/web/src/components/shared/require-auth.tsx` | **MODIFY** | Redirect to Keycloak login when SSO enabled + not authenticated |
| 17 | `ui/web/src/stores/use-ui-store.ts` | **MODIFY** | Add `ssoEnabled` flag |
| 18 | `ui/web/src/App.tsx` | **MODIFY** | Fetch SSO status on startup |
| 19 | `ui/web/src/pages/login/login-page.tsx` | **MODIFY** | Show "Login with Keycloak" button when SSO enabled |
| **Frontend — i18n** | | | |
| 20 | `ui/web/src/i18n/locales/en/sso.json` | **NEW** | English SSO strings |
| 21 | `ui/web/src/i18n/locales/vi/sso.json` | **NEW** | Vietnamese SSO strings |
| 22 | `ui/web/src/i18n/locales/zh/sso.json` | **NEW** | Chinese SSO strings |
| 23 | `ui/web/src/i18n/index.ts` | **MODIFY** | Add `sso` namespace |

**Total:** 11 new files, 12 modified files. No database migrations needed (mapping table is in external Talita DB).

---

## Execution Phases

### Phase 1: Backend SSO Infrastructure (no frontend changes)

**Goal:** Backend can handle OIDC flow, query Talita DB, and return credentials via cookie exchange.

1. Add `go.mod` dependencies (`go-oidc/v3`, promote `oauth2`)
2. Create `internal/config/config_sso.go` — SSOConfig struct
3. Modify `internal/config/config.go` — add SSO field
4. Modify `internal/config/config_load.go` — env var overlay
5. Create `internal/sso/oidc.go` — OIDC client
6. Create `internal/sso/mapping.go` — external DB connection
7. Create `internal/sso/cookie.go` — signed cookie helpers
8. Create `internal/http/sso_handler.go` — HTTP handler with 4 routes
9. Modify `internal/gateway/server.go` — add ssoHandler, register routes
10. Modify `internal/webui/handler.go` — add `/auth/` to apiPrefixes
11. Modify `cmd/gateway.go` — SSO initialization at startup

**Verification:**
```bash
go build ./...                      # PG build compiles
go build -tags sqliteonly ./...     # SQLite build compiles (SSO is standard-only)
go vet ./...
```

Manual test with curl:
```bash
# SSO status (should return {"enabled": true} when env vars set)
curl http://localhost:8080/v1/auth/sso/status

# Login redirect (should 302 to Keycloak)
curl -v http://localhost:8080/auth/keycloak/login
```

### Phase 2: Frontend SSO Integration

**Goal:** Frontend handles SSO redirect, callback, and unauthorized page.

1. Create `ui/web/src/pages/sso/sso-callback-page.tsx`
2. Create `ui/web/src/pages/sso/unauthorized-page.tsx`
3. Modify `ui/web/src/lib/routes.ts` — add route constants
4. Modify `ui/web/src/routes.tsx` — register new routes
5. Modify `ui/web/src/stores/use-ui-store.ts` — add ssoEnabled
6. Modify `ui/web/src/App.tsx` — fetch SSO status on startup
7. Modify `ui/web/src/components/shared/require-auth.tsx` — SSO redirect
8. Modify `ui/web/src/pages/login/login-page.tsx` — Keycloak login button
9. Create i18n files (en/vi/zh `sso.json`)
10. Modify `ui/web/src/i18n/index.ts` — add sso namespace

**Verification:**
```bash
cd ui/web && pnpm build    # No TypeScript errors
cd ui/web && pnpm dev      # Manual test full flow
```

### Phase 3: End-to-End Testing

**Goal:** Verify the complete SSO flow works with real Keycloak + Talita DB.

1. Configure Keycloak realm + client (`la-claw`)
2. Set up `user_mapping_goclaw` table in Talita DB with test data
3. Set all env vars in `.env.local`
4. Start GoClaw with SSO enabled
5. Test flow:
   - Access GoClaw → redirected to Keycloak
   - Login with valid credentials → redirected back → mapping found → enter app
   - Login with valid credentials but no mapping → unauthorized page
   - Login with invalid credentials → Keycloak shows error

---

## Edge Cases & Considerations

### 1. SSO Disabled (Default)
**Scenario:** `GOCLAW_SSO_ENABLED` not set or `false`.
**Handling:** SSO handler is `nil`, no SSO routes registered, frontend `ssoEnabled=false`, existing token/pairing login works unchanged. Zero impact on existing behavior.

### 2. Keycloak Unreachable
**Scenario:** Keycloak server is down during OIDC discovery or token exchange.
**Handling:** `NewOIDCClient()` fails at startup → `slog.Error` + `os.Exit(1)`. If Keycloak goes down after startup, `ExchangeCode()` returns error → user redirected to `/unauthorized`. Consider adding a health check with retry logic in future.

### 3. Talita DB Unreachable
**Scenario:** Talita DB (10.24.117.58) is down during user mapping lookup.
**Handling:** `LookupByEmail()` returns error → user redirected to `/unauthorized`. The error is logged with `slog.Error`. Connection pool has retry built into `pgx`. Consider adding connection health check.

### 4. Cookie Expired Before Exchange
**Scenario:** User takes >30 seconds between callback redirect and frontend exchange call (e.g., slow network).
**Handling:** `ReadSessionCookie()` returns error (expired) → exchange endpoint returns 401 → frontend redirects to `/unauthorized`. User can retry by accessing GoClaw again (triggers new SSO flow).

### 5. Multiple Tabs / Concurrent SSO Flows
**Scenario:** User opens GoClaw in two tabs simultaneously, both trigger SSO.
**Handling:** Each tab gets its own OIDC flow (separate state + PKCE cookies). The state cookie is overwritten by the second tab, but this only affects the first tab's callback (state mismatch → unauthorized). The second tab completes normally. This is acceptable — users typically don't open multiple tabs during login.

### 6. Token in Mapping Table is Invalid
**Scenario:** The GoClaw token in `user_mapping_goclaw` is expired, revoked, or incorrect.
**Handling:** SSO flow succeeds (mapping found), but subsequent WS connect fails with `ErrUnauthorized`. The frontend's `WsProvider` `onAuthFailure` callback triggers `logout()`, redirecting to login. If SSO is enabled, this redirects back to Keycloak. The user enters a loop if the token is permanently invalid. **Mitigation:** Admin must ensure tokens in the mapping table are valid. Consider adding a token validation step in the callback handler (query GoClaw DB to verify token exists).

### 7. Desktop Edition (Lite)
**No changes needed.** Desktop uses zero-login model (auto-generated token in keyring). SSO is a Standard edition feature. The `GOCLAW_SSO_ENABLED` env var is not set in desktop mode. SSO handler is `nil`, no impact.

### 8. API Access (Non-Browser Clients)
**Scenario:** CLI tools, API integrations, or other non-browser clients need to access GoClaw API.
**Handling:** SSO only affects browser-based web UI access. API clients continue to use existing auth (gateway token, API keys). The SSO routes (`/auth/keycloak/*`) are browser-oriented (redirect-based). API clients don't interact with SSO.

### 9. Return URL Preservation
**Scenario:** User bookmarks `https://goclaw.example.com/agents/some-agent` and accesses it directly.
**Handling:** `RequireAuth` captures `location.pathname + location.search` and passes it as `return_to` query param to `/auth/keycloak/login`. After SSO completes, the frontend navigates to the original URL. The `return_to` param is stored in the state cookie and passed through the OIDC flow.

**Implementation note:** The `return_to` value should be validated to prevent open redirect attacks. Only allow relative paths (starting with `/`), reject absolute URLs.

### 10. Keycloak Session / Single Logout
**Future consideration:** Currently, logging out of GoClaw (`logout()` in auth store) clears the local token but doesn't log out from Keycloak. Users can re-enter without re-authenticating if their Keycloak session is still active. To implement single logout, add a `/auth/keycloak/logout` endpoint that redirects to Keycloak's `end_session_endpoint` (discovered via OIDC well-known config). This is a Phase 4 enhancement.

### 11. Role Mapping
**Current design:** The mapped GoClaw token determines the user's role (admin if gateway token, operator if API key with write scope, etc.). Keycloak roles/groups are NOT mapped to GoClaw roles. If role mapping is needed in the future, the `user_mapping_goclaw` table could include a `role` column, or Keycloak group claims could be read from the ID token.

### 12. Mobile Compatibility
All new frontend pages follow CLAUDE.md mobile rules:
- `h-dvh` for viewport height (not `h-screen`)
- No inputs on unauthorized page (no `text-base` concern)
- Centered layout works on mobile
- SSO callback page is a loading spinner — no user interaction needed

---

## Security Considerations

| Concern | Mitigation |
|---------|------------|
| **CSRF** | OIDC `state` parameter, stored in signed cookie, verified on callback |
| **Code interception** | PKCE (S256) — `code_verifier` generated per-request, `code_challenge` sent to Keycloak, verified during token exchange |
| **Cookie tampering** | HMAC-SHA256 signed cookie using gateway token as key |
| **Cookie theft (XSS)** | Cookie is `HttpOnly` — not accessible via JavaScript |
| **Cookie over network** | Cookie is `Secure` — only sent over HTTPS (disabled in dev mode) |
| **Open redirect** | `return_to` parameter validated — only relative paths allowed |
| **Token in URL** | Token is NOT passed via URL — uses HttpOnly cookie exchange |
| **Talita DB credentials** | Stored in env vars only (`json:"-"`), never in config.json |
| **Keycloak client secret** | Stored in env vars only (`json:"-"`), never in config.json |
| **SQL injection** | Parameterized query (`$1`) for email lookup |
| **Token logging** | GoClaw token is never logged. Email is logged at `slog.Warn` level only on mapping failure (for debugging) |

---

## Keycloak Configuration Guide

### Realm Setup
1. Create or select a realm in Keycloak (e.g., `dev`)
2. The OIDC issuer URL is: `https://larasati.lintasarta.co.id/realms/dev`
3. Set `GOCLAW_SSO_KEYCLOAK_ISSUER=https://larasati.lintasarta.co.id/realms/dev` in `.env.local`

### Client Configuration
1. **Client ID:** `la-claw`
2. **Client Protocol:** `openid-connect`
3. **Access Type:** `confidential` (requires client secret)
4. **Client Secret:** Generated by Keycloak — copy to `GOCLAW_SSO_CLIENT_SECRET` env var
5. **Valid Redirect URIs:** `https://goclaw.example.com/auth/keycloak/callback`
6. **Web Origins:** `https://goclaw.example.com`
7. **Standard Flow Enabled:** `ON` (Authorization Code flow)
8. **Direct Access Grants Enabled:** `OFF` (no password grant)
9. **Implicit Flow Enabled:** `OFF`
10. **PKCE Code Challenge Method:** `S256` (if configurable in Keycloak version)

### Mappers (Claims)
Ensure the following mappers are configured for the `la-claw` client:
1. **email** — built-in, included in ID token by default
2. **preferred_username** — built-in, included in ID token by default
3. **name** — built-in, included in ID token by default

If `email` is not in the ID token by default, add a mapper:
- **Name:** `email`
- **Mapper Type:** `User Property`
- **Property:** `email`
- **Token Claim Name:** `email`
- **Add to ID token:** `ON`
- **Add to access token:** `ON`

### Talita DB Table Setup
```sql
CREATE TABLE user_mapping_goclaw (
    email    VARCHAR(255) PRIMARY KEY,
    username VARCHAR(255) NOT NULL,
    token    TEXT NOT NULL
);

-- Example data:
INSERT INTO user_mapping_goclaw (email, username, token) VALUES
('admin@company.com', 'admin', 'goclaw_abc123...'),
('user1@company.com', 'user1', 'goclaw_def456...');
```

The `token` column should contain a valid GoClaw API key (`goclaw_<32hex>`) or the gateway token. These must be pre-provisioned in GoClaw before users can log in via SSO.

---

## Files NOT Changed (and why)

| File | Why |
|------|-----|
| `internal/gateway/router.go` | WS connect auth unchanged — SSO provides the token, WS connect uses it as before |
| `internal/http/auth.go` | HTTP auth middleware unchanged — SSO provides the token, Bearer auth uses it as before |
| `internal/permissions/policy.go` | RBAC unchanged — role determined by token type, not SSO |
| `internal/store/` | No GoClaw DB schema changes — mapping is in external Talita DB |
| `migrations/` | No PostgreSQL migrations needed |
| `internal/store/sqlitestore/` | No SQLite schema changes — SSO is Standard edition only |
| `ui/desktop/` | Desktop uses zero-login model, SSO not applicable |
| `internal/crypto/apikey.go` | API key generation unchanged |
| `internal/oauth/openai.go` | Existing OAuth is for LLM providers, unrelated to SSO |
| `cmd/onboard_helpers.go` | Onboard wizard unchanged — SSO is configured via env vars |

---

## Dependency Summary

### New Go Dependencies
| Package | Version | Purpose |
|---------|---------|---------|
| `github.com/coreos/go-oidc/v3` | latest | OIDC client: discovery, ID token verification, userinfo |
| `golang.org/x/oauth2` | v0.34.0 (existing indirect → promote to direct) | OAuth2 config + token exchange |

### No New Frontend Dependencies
All frontend changes use existing libraries (React, React Router, Zustand, lucide-react, react-i18next).

---

## Post-Implementation Checklist

```bash
# Go checks
go fix ./...
go build ./...                      # PG build
go build -tags sqliteonly ./...     # SQLite build (SSO code should be no-op)
go vet ./...

# Frontend checks
cd ui/web && pnpm install
cd ui/web && pnpm build             # TypeScript compile check
cd ui/web && pnpm test              # Existing tests pass

# Manual E2E test
# 1. Set all SSO env vars in .env.local
# 2. Start GoClaw
# 3. Access web UI in browser
# 4. Verify redirect to Keycloak
# 5. Login with valid user → verify mapping → verify app access
# 6. Login with unmapped user → verify unauthorized page
```
