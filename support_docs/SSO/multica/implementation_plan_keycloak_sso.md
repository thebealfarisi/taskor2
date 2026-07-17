# Implementation Plan: Keycloak SSO Integration via OpenID Connect (Multica)

**Date:** 2026-07-17 (revised after alignment audit against the actual codebase)
**Objective:** Integrate Keycloak (Larasati) as an SSO identity provider using the OpenID Connect (OIDC) Authorization Code flow with PKCE for Multica. Users authenticate through Keycloak; the backend extracts the email from the verified ID token and maps it to a Multica internal user via the **existing** `findOrCreateUser` helper. Access is gated by the **existing** signup restrictions (`ALLOW_SIGNUP` / `ALLOWED_EMAIL_DOMAINS` / `ALLOWED_EMAILS`) — there is **no external Talita mapping database** and **no new mapping table**. The **primary model is a domain whitelist** (`ALLOWED_EMAIL_DOMAINS`), which scales to thousands of users without per-email maintenance: anyone with a Keycloak account in the whitelisted domain may enter. `ALLOWED_EMAILS` remains available as an optional per-individu exception list for emails outside the whitelisted domain(s). Existing users always pass regardless of the allowlists. Everyone else is rejected with a 403-style redirect back to the login page with an error flag.

> **Key difference from the GoClaw SSO plan (`support_docs/SSO/goclaw/`):** GoClaw stores tokens in `localStorage` and therefore needed a signed SSO session cookie + a frontend `/sso/callback` page + a `POST /v1/auth/sso/exchange` endpoint to ferry the token from backend to frontend. **Multica already authenticates with a JWT in an HttpOnly cookie** (`multica_auth`, set by `auth.SetAuthCookies`). The Keycloak callback can therefore set that same cookie directly and redirect to the app — **no exchange dance, no `/sso/callback` page, no `/unauthorized` page, and no Talita DB**.

---

## Architecture Overview

### Current Auth System (verified against the codebase)

Multica authenticates with a **JWT (HS256, signed with `JWT_SECRET`) stored in an HttpOnly cookie** named `multica_auth`, paired with a readable `multica_csrf` cookie. Both are set by `auth.SetAuthCookies(w, token)` in `server/internal/auth/cookie.go` and carry `SameSite=Strict`, `Secure` (derived from `FRONTEND_ORIGIN` scheme), and a 30-day TTL (`AUTH_TOKEN_TTL`).

Existing auth entry points (all in `server/internal/handler/auth.go`, registered in `server/cmd/server/router.go:688-691`):

| Route | Handler | Mechanism |
|-------|---------|-----------|
| `POST /auth/send-code` | `SendCode` | Email magic-link code |
| `POST /auth/verify-code` | `VerifyCode` | Validates code → `findOrCreateUser` → `issueJWT` → `SetAuthCookies` |
| `POST /auth/google` | `GoogleLogin` | Exchanges Google code → `findOrCreateUser` → `issueJWT` → `SetAuthCookies` |
| `POST /auth/logout` | `Logout` | `ClearAuthCookies` |

Note the route convention: **auth routes live under `/auth/*` with no `/api` or `/v1` prefix.** Public API routes live under `/api/*` (e.g. `GET /api/config`). Protected routes use `middleware.Auth`.

`findOrCreateUser(ctx, email)` (`auth.go:167`) looks up `GetUserByEmail`; for new emails it calls `checkSignupAllowed` (`auth.go:225`) which enforces, in order: explicit `ALLOWED_EMAILS` whitelist → `ALLOWED_EMAIL_DOMAINS` domain whitelist → `ALLOW_SIGNUP` flag → deny if any allowlist is set but unmatched. Existing users always pass. This is the **exact** gate the SSO flow will reuse — no new access-control code is needed.

> **Scaling to hundreds/thousands of users:** the primary model is `ALLOWED_EMAIL_DOMAINS` (e.g. `lintasarta.co.id`). One env line covers every user in that domain — no per-email maintenance. Use `ALLOWED_EMAILS` only for individual exceptions outside the domain (contractors, vendors). For fully closed setups with no domain sharing, set `ALLOW_SIGNUP=false` with no allowlists and pre-provision users in the `user` table; existing users always pass.

The web UI (`apps/web`) is a Next.js App Router app. The login page `apps/web/app/(auth)/login/page.tsx` is a thin wrapper that renders `<LoginPage>` from `@multica/views/auth` (`packages/views/auth/login-page.tsx`). It reads `googleClientId` from `useConfigStore` (`packages/core/config/index.ts`), which is populated from `GET /api/config` (`handler.GetConfig` in `server/internal/handler/config.go`, returning `AppConfig`). On boot, `AuthInitializer` (`packages/core/platform/auth-initializer.tsx`) calls `useAuthStore.initialize()` → `api.getMe()`, relying on the HttpOnly cookie being sent automatically. The auth store (`packages/core/auth/store.ts`) has **no `setCredentials` method** — cookie mode just calls `getMe()`.

**Key insight:** SSO does **not** replace the existing cookie auth. It provides a **new way to obtain** the `multica_auth` cookie — via Keycloak. Once the cookie is set, the existing flow (`AuthInitializer` → `getMe` → React Query) works unchanged.

### Proposed SSO Flow

```
┌─────────┐     ┌───────────┐     ┌──────────┐     ┌────────────┐
│  User   │────▶│  Multica  │────▶│ Keycloak │────▶│  Multica   │
│ clicks  │     │  backend  │     │  login   │     │  callback  │
│ "Login  │     │ /auth/    │     │ page     │     │ /auth/     │
│  with   │     │ keycloak/ │     │ (Larasati)│     │ keycloak/  │
│ Keycloak"│    │ login     │     │          │     │ callback   │
└─────────┘     └───────────┘     └──────────┘     └────────────┘
   window.location                  302 to KC         │
   = "/auth/keycloak/login"         authz endpoint     │
                                                       ▼
                                    ┌─────────────────────────────┐
                                    │ verify state (state cookie) │
                                    │ exchange code → ID token    │
                                    │ extract email (PKCE)        │
                                    │ findOrCreateUser(email)     │
                                    └─────────────────────────────┘
                                          │            │
                                   allowed            ErrSignupProhibited
                                          │            │
                                          ▼            ▼
                                   issueJWT +      redirect to
                                   SetAuthCookies  /login?error=signup_prohibited
                                   redirect to /   (login page shows message)
                                          │
                                          ▼
                                   AuthInitializer → getMe() (cookie sent)
                                   → user loaded → app
```

### Detailed Flow Steps

1. **User opens Multica** (`/` or any protected route). `AuthInitializer` runs `initialize()` → `getMe()` → no `multica_auth` cookie → user is `null`.
2. **Frontend redirects to `/login`** (existing behaviour). The login page reads `ssoEnabled` from `useConfigStore` (populated from `GET /api/config`).
3. **If `ssoEnabled` is true**, the login page renders a **"Login with Keycloak"** button. Clicking it runs `window.location.href = "/auth/keycloak/login?next=" + encodeURIComponent(returnTo)`.
4. **Backend `GET /auth/keycloak/login`** generates a PKCE `code_verifier` + random `state`, stores both in a short-lived **`SameSite=Lax`** signed HttpOnly cookie (`multica_sso_state`), and 302-redirects to the Keycloak authorization endpoint (discovered via the issuer's `/.well-known/openid-configuration`).
5. **User authenticates on Keycloak (Larasati)** with username/password.
6. **Keycloak 302-redirects** to `GET /auth/keycloak/callback?code=...&state=...`.
7. **Backend `GET /auth/keycloak/callback`:**
   - Reads the `multica_sso_state` cookie, verifies the HMAC signature, and checks `state` matches the query param (CSRF protection).
   - Exchanges `code` for tokens using the stored `code_verifier` (PKCE verified by Keycloak).
   - Verifies the ID token signature (via OIDC discovery JWKS) and extracts the `email` claim (userinfo endpoint fallback).
   - Calls `findOrCreateUser(ctx, email)` — the **same** helper used by `VerifyCode` and `GoogleLogin`. This enforces the operator's signup restrictions.
   - **If allowed:** `issueJWT(user)` → `auth.SetAuthCookies(w, token)` (sets `multica_auth` + `multica_csrf`, `SameSite=Strict`) → clears the state cookie → 302-redirect to the `next` URL (sanitized, relative only) or `/`.
   - **If `ErrSignupProhibited`:** clears the state cookie → 302-redirect to `/login?error=signup_prohibited`.
   - **On any other error** (state mismatch, token exchange failure, network): clears the state cookie → 302-redirect to `/login?error=sso_failed`.
8. **Frontend at `/` (or `next`):** `AuthInitializer` runs `initialize()` → `getMe()`. The `multica_auth` cookie is sent automatically (same-site navigation) → user loaded → React Query hydrates → app renders.
9. **If redirected to `/login?error=...`:** the login page reads the `error` search param and surfaces a message ("Your account is not authorized. Please contact Admin IT." for `signup_prohibited`; "SSO failed, please try again." for `sso_failed`), and still offers the fallback login form (if any).
10. **Subsequent requests** use the existing Multica auth flow unchanged (cookie + CSRF header, `middleware.Auth`, WebSocket cookie auth).

> **Why no `/sso/callback` page and no exchange endpoint?** Multica's auth cookie is already HttpOnly and is set directly by the backend callback. There is nothing to ferry from backend to frontend. The `AuthInitializer` already boots every page load by calling `getMe()` with the cookie. This is the single biggest simplification versus the GoClaw plan and removes 3 deliverables (session cookie, `/sso/callback` page, `POST /v1/auth/sso/exchange`).

> **Why no `/unauthorized` page?** The chosen mapping model reuses the existing signup-restriction gate. A rejected email is a configuration/whitelist decision, surfaced as a redirect to the login page with an `error` flag — consistent with how `VerifyCode`/`GoogleLogin` already return `403 ErrSignupProhibited`. A dedicated error route would be dead code for this model.

---

## Configuration & Environment Variables

### New Environment Variables

Add to `.env` (or system environment). The OIDC values are **env-only** and never serialized into any config file.

```bash
# ─── Keycloak SSO Configuration ───
MULTICA_SSO_ENABLED=true
MULTICA_SSO_KEYCLOAK_ISSUER=https://larasati.lintasarta.co.id/realms/dev
MULTICA_SSO_CLIENT_ID=task-or
MULTICA_SSO_CLIENT_SECRET=oV6mcQShpvBtogbTGgGkuzKZ09Fy1o3X
# Redirect URI = the BACKEND callback URL (same origin as the auth routes).
# In same-origin deployments this is {FRONTEND_ORIGIN}/auth/keycloak/callback.
MULTICA_SSO_REDIRECT_URL=http://localhost:3000/auth/keycloak/callback
# Skip TLS certificate verification for OIDC discovery + token exchange.
# Set "true" for internal CA environments (e.g. Larasati self-hosted Keycloak
# with a private CA not in the system trust store). Leave false in production
# with a publicly trusted certificate.
MULTICA_SSO_SKIP_TLS_VERIFY=true

# ─── Access control: domain whitelist (reuse EXISTING signup-restriction env vars) ───
# Primary model: allow an entire email domain. Scales to thousands of users
# without per-email maintenance — anyone with a Keycloak account in the
# whitelisted domain may enter. Set ALLOW_SIGNUP=false so users OUTSIDE the
# domain are rejected.
ALLOW_SIGNUP=false
ALLOWED_EMAIL_DOMAINS=lintasarta.co.id
# (Optional) ALLOWED_EMAILS=contractor1@vendor.com,contractor2@vendor.com
#            — per-individu exceptions for emails OUTSIDE the whitelisted domain(s).
#            Comma-separated, case-insensitive. Existing users always pass regardless.
#
# IMPORTANT — how the two allowlists combine (see checkSignupAllowed, auth.go:225):
#   • ALLOWED_EMAILS match  → allow (checked first, wins)
#   • ALLOWED_EMAIL_DOMAINS match → allow (checked second)
#   • neither matches + ALLOW_SIGNUP=false → ErrSignupProhibited (403)
# The combination is a UNION (OR), NOT an exception/denylist. Setting both
# does NOT exclude specific emails inside a whitelisted domain — an email in
# the whitelisted domain is always allowed regardless of ALLOWED_EMAILS.
# To block a specific email inside a whitelisted domain you must either remove
# the domain from ALLOWED_EMAIL_DOMAINS and list every allowed email in
# ALLOWED_EMAILS, or pre-provision only the allowed users in the `user` table
# (existing users always pass). A DENIED_EMAILS denylist is NOT supported by
# the current code and would require a small change to checkSignupAllowed.
```

> **Issuer note:** `MULTICA_SSO_KEYCLOAK_ISSUER` is the full Keycloak realm URL. The `go-oidc` library appends `/.well-known/openid-configuration` automatically, i.e. `https://larasati.lintasarta.co.id/realms/dev/.well-known/openid-configuration`. No separate base-URL/realm config is needed.

> **Redirect URL note:** The redirect URI must point at the **backend** route `/auth/keycloak/callback`, registered on the Chi router (see Backend Changes). In same-origin deployments (Next.js proxies or backend == frontend origin) this is `{origin}/auth/keycloak/callback`. The exact value must also be registered as a **Valid Redirect URI** in the Keycloak `task-or` client.

### Config wiring (no new config package)

The project has **no `server/internal/config/` package**. Config is the `handler.Config` struct in `server/internal/handler/handler.go:57`, constructed in `server/cmd/server/router.go:166` (`signupConfig`) by reading `os.Getenv` directly. SSO config follows the same pattern.

**File:** `server/internal/handler/handler.go` (MODIFY — `Config` struct, ~line 57)

Add SSO fields to the existing `Config` struct:

```go
type Config struct {
    // ... existing fields (AllowSignup, AllowedEmails, ...) ...
    LLMAPIKey       string
    LLMBaseURL      string
    LLMDefaultModel string

    // SSO / Keycloak (env-only). All zero values when SSO is disabled.
    SSOEnabled        bool
    SSOIssuer         string
    SSOClientID       string
    SSOClientSecret   string
    SSORedirectURL    string
    SSOSkipTLSVerify  bool
}
```

**File:** `server/cmd/server/router.go` (MODIFY — `signupConfig` literal, ~line 166)

```go
signupConfig := handler.Config{
    // ... existing fields ...
    SSOEnabled:      os.Getenv("MULTICA_SSO_ENABLED") == "true",
    SSOIssuer:       strings.TrimSpace(os.Getenv("MULTICA_SSO_KEYCLOAK_ISSUER")),
    SSOClientID:     strings.TrimSpace(os.Getenv("MULTICA_SSO_CLIENT_ID")),
    SSOClientSecret: strings.TrimSpace(os.Getenv("MULTICA_SSO_CLIENT_SECRET")),
    SSORedirectURL:  strings.TrimSpace(os.Getenv("MULTICA_SSO_REDIRECT_URL")),
    SSOSkipTLSVerify: os.Getenv("MULTICA_SSO_SKIP_TLS_VERIFY") == "true",
}
```

### Public config endpoint (SSO enabled flag for the frontend)

The frontend learns runtime flags from `GET /api/config` (`handler.GetConfig`, `server/internal/handler/config.go`), which returns `AppConfig`. Add an `sso_enabled` field so the login page can render the Keycloak button without a separate status endpoint.

**File:** `server/internal/handler/config.go` (MODIFY — `AppConfig` struct + `GetConfig`)

```go
type AppConfig struct {
    // ... existing fields ...
    GoogleClientID string `json:"google_client_id,omitempty"`
    // SSOEnabled tells the web app to render the "Login with Keycloak" button.
    // Omitted when false to keep responses identical for SSO-disabled deployments.
    SSOEnabled bool `json:"sso_enabled,omitempty"`
    // ... rest ...
}

// Inside GetConfig:
config.SSOEnabled = os.Getenv("MULTICA_SSO_ENABLED") == "true"
```

---

## Backend Changes

### New Package: `server/internal/sso/`

#### 1. `server/internal/sso/oidc.go` (NEW)

OIDC client: discovery, authorization URL generation, token exchange, email extraction. Uses `github.com/coreos/go-oidc/v3/oidc` + `golang.org/x/oauth2`.

```go
package sso

import (
    "context"
    "crypto/rand"
    "encoding/base64"

    "github.com/coreos/go-oidc/v3/oidc"
    "golang.org/x/oauth2"
)

// OIDCClient wraps the OIDC provider + OAuth2 config for Keycloak.
type OIDCClient struct {
    provider *oidc.Provider
    oauth2   *oauth2.Config
    verifier *oidc.IDTokenVerifier
}

// NewOIDCClient discovers Keycloak endpoints via {issuer}/.well-known/openid-configuration.
func NewOIDCClient(ctx context.Context, issuer, clientID, clientSecret, redirectURL string, skipTLSVerify bool) (*OIDCClient, error) {
    // When skipTLSVerify is true, use a custom HTTP client with
    // InsecureSkipVerify for OIDC discovery + token exchange + userinfo.
    // Intended for internal CA environments (e.g. Larasati).
    provider, err := oidc.NewProvider(ctx, issuer)
    if err != nil {
        return nil, err
    }
    return &OIDCClient{
        provider: provider,
        oauth2: &oauth2.Config{
            ClientID:     clientID,
            ClientSecret: clientSecret,
            Endpoint:     provider.Endpoint(),
            RedirectURL:  redirectURL,
            Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
        },
        verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
    }, nil
}

// AuthURL builds the Keycloak authorization URL with PKCE (S256) + state.
func (c *OIDCClient) AuthURL(state, codeVerifier string) string {
    return c.oauth2.AuthCodeURL(state,
        oauth2.SetAuthURLParam("code_challenge", oidc.S256ChallengeFromVerifier(codeVerifier)),
        oauth2.SetAuthURLParam("code_challenge_method", "S256"),
    )
}

// ExchangeCode exchanges the auth code for tokens, verifies the ID token,
// and returns the email claim (userinfo fallback if absent from the ID token).
func (c *OIDCClient) ExchangeCode(ctx context.Context, code, codeVerifier string) (email string, err error)

// GenerateCodeVerifier returns a PKCE code_verifier (43-128 chars, base64url).
func GenerateCodeVerifier() (string, error)

// GenerateState returns a random state parameter for CSRF protection.
func GenerateState() (string, error)
```

**Design notes:**
- OIDC discovery via the issuer URL — `go-oidc` resolves all Keycloak endpoints automatically.
- PKCE (S256) is mandatory even though a client secret is used (defense-in-depth).
- `state` prevents CSRF; stored in a short-lived cookie and verified on callback.
- Email is read from the ID token `email` claim; falls back to the userinfo endpoint if the claim is missing (some Keycloak configs omit it from the ID token by default).

#### 2. `server/internal/sso/state.go` (NEW)

Signed, short-lived **state cookie** (`multica_sso_state`) carrying `{state, code_verifier, next, exp}`. This is the OIDC-flow state cookie only — **not** a session-token cookie (unlike the GoClaw plan's `cookie.go`).

```go
package sso

// CookieName is the OIDC state cookie.
const CookieName = "multica_sso_state"
// CookieTTL is the max lifetime of the state cookie (5 minutes).
const CookieTTL = 5 * time.Minute

// StatePayload is the data encoded (HMAC-SHA256 signed) in the state cookie.
type StatePayload struct {
    State        string `json:"state"`
    CodeVerifier string `json:"code_verifier"`
    Next         string `json:"next,omitempty"` // sanitized relative path
    Exp          int64  `json:"exp"`
}

// SetStateCookie sets the signed, HttpOnly, SameSite=Lax state cookie.
// Signing key = JWT_SECRET (reuse the existing auth secret).
func SetStateCookie(w http.ResponseWriter, payload StatePayload, signingKey string) error

// ReadStateCookie reads + verifies the state cookie.
func ReadStateCookie(r *http.Request, signingKey string) (*StatePayload, error)

// ClearStateCookie deletes the state cookie.
func ClearStateCookie(w http.ResponseWriter)
```

**Critical SameSite detail:** The state cookie **must** be `SameSite=Lax` (not `Strict`). The OIDC callback is a top-level cross-site GET redirect from Keycloak back to Multica; `SameSite=Strict` cookies are **not** sent on cross-site navigations, which would break state verification. `Lax` permits cookies on top-level GET navigations — exactly the callback case. The existing `multica_auth` cookie stays `Strict` (it is only *set* during the callback, never *read* cross-site; on the subsequent same-site redirect to `/` it is sent normally).

### HTTP Handler (reuse existing `Handler`)

Add Keycloak methods to the existing `handler.Handler` (the same struct that owns `findOrCreateUser`, `issueJWT`, `SetAuthCookies`). This reuses all existing auth plumbing instead of building a parallel `SSOHandler`.

**File:** `server/internal/handler/sso.go` (NEW)

```go
package handler

import (
    "net/http"
    "strings"

    "github.com/multica-ai/multica/server/internal/analytics"
    "github.com/multica-ai/multica/server/internal/auth"
    "github.com/multica-ai/multica/server/internal/sso"
    db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// KeycloakLogin initiates the OIDC authorization code flow.
func (h *Handler) KeycloakLogin(w http.ResponseWriter, r *http.Request) {
    if !h.cfg.SSOEnabled || h.OIDC == nil {
        http.NotFound(w, r)
        return
    }
    codeVerifier, _ := sso.GenerateCodeVerifier()
    state, _ := sso.GenerateState()
    next := sanitizeNext(r.URL.Query().Get("next")) // relative paths only
    sso.SetStateCookie(w, sso.StatePayload{
        State: state, CodeVerifier: codeVerifier, Next: next,
        Exp: time.Now().Add(sso.CookieTTL).Unix(),
    }, auth.JWTSecret())
    http.Redirect(w, r, h.OIDC.AuthURL(state, codeVerifier), http.StatusFound)
}

// KeycloakCallback handles the OIDC callback from Keycloak.
func (h *Handler) KeycloakCallback(w http.ResponseWriter, r *http.Request) {
    if !h.cfg.SSOEnabled || h.OIDC == nil {
        http.NotFound(w, r)
        return
    }
    // 1. Verify state cookie.
    sp, err := sso.ReadStateCookie(r, auth.JWTSecret())
    if err != nil || sp.State != r.URL.Query().Get("state") {
        sso.ClearStateCookie(w)
        redirectLogin(w, r, "sso_failed")
        return
    }
    // 2. Exchange code for ID token, extract email.
    email, err := h.OIDC.ExchangeCode(r.Context(), r.URL.Query().Get("code"), sp.CodeVerifier)
    if err != nil {
        sso.ClearStateCookie(w)
        redirectLogin(w, r, "sso_failed")
        return
    }
    // 3. Map to Multica user (reuses the existing signup gate).
    user, isNew, err := h.findOrCreateUser(r.Context(), strings.ToLower(strings.TrimSpace(email)))
    if err != nil {
        sso.ClearStateCookie(w)
        var signupErr SignupError
        if errors.As(err, &signupErr) {
            redirectLogin(w, r, "signup_prohibited") // 403-style: not whitelisted
            return
        }
        redirectLogin(w, r, "sso_failed")
        return
    }
    // 4. Issue JWT + set the existing HttpOnly auth cookies.
    tokenString, err := h.issueJWT(user)
    if err != nil {
        sso.ClearStateCookie(w)
        redirectLogin(w, r, "sso_failed")
        return
    }
    if err := auth.SetAuthCookies(w, tokenString); err != nil {
        slog.Warn("sso: failed to set auth cookies", "error", err)
    }
    if h.CFSigner != nil {
        for _, c := range h.CFSigner.SignedCookies(time.Now().Add(auth.AuthTokenTTL())) {
            http.SetCookie(w, c)
        }
    }
    sso.ClearStateCookie(w)
    if isNew {
        evt := analytics.Signup(uuidToString(user.ID), user.Email, signupSourceFromRequest(r))
        evt.Properties["auth_method"] = "keycloak"
        obsmetrics.RecordEvent(h.Analytics, h.Metrics, evt)
    }
    slog.Info("user logged in via keycloak",
        append(logger.RequestAttrs(r), "user_id", uuidToString(user.ID), "email", user.Email)...)
    // 5. Redirect to the originally requested page (sanitized) or /.
    dest := sp.Next
    if dest == "" {
        dest = "/"
    }
    http.Redirect(w, r, dest, http.StatusFound)
}

// redirectLogin redirects to /login?error=<reason>.
func redirectLogin(w http.ResponseWriter, r *http.Request, reason string) {
    http.Redirect(w, r, "/login?error="+reason, http.StatusFound)
}
```

**Handler struct wiring** (`server/internal/handler/handler.go` MODIFY — add field):

```go
type Handler struct {
    // ... existing fields ...
    OIDC *sso.OIDCClient // nil when SSO disabled
}
```

**Construction** (`server/cmd/server/router.go` MODIFY — after `h := handler.New(...)`, ~line 182):

```go
if signupConfig.SSOEnabled {
oidcClient, err := sso.NewOIDCClient(ctx,
        cfg.SSO.KeycloakIssuer,
        cfg.SSO.ClientID,
        cfg.SSO.ClientSecret,
        cfg.SSO.RedirectURL,
        cfg.SSO.SkipTLSVerify,
    )
    if err != nil {
        slog.Error("sso: keycloak oidc init failed", "error", err)
        os.Exit(1)
    }
    h.OIDC = oidcClient
    slog.Info("sso: keycloak oidc enabled", "issuer", signupConfig.SSOIssuer)
}
```

### Route Registration

**File:** `server/cmd/server/router.go` (MODIFY — public auth group, ~line 688)

Register the Keycloak routes alongside the existing public auth routes, with the same per-IP rate limiting:

```go
r.With(authRL).Post("/auth/send-code", h.SendCode)
r.With(authVerifyRL).Post("/auth/verify-code", h.VerifyCode)
r.With(authRL).Post("/auth/google", h.GoogleLogin)
r.Post("/auth/logout", h.Logout)
// Keycloak SSO (public — handle the auth flow itself; no-op when disabled)
r.With(authRL).Get("/auth/keycloak/login", h.KeycloakLogin)
r.With(authRL).Get("/auth/keycloak/callback", h.KeycloakCallback)
```

> Routes follow the existing `/auth/*` convention (no `/api` or `/v1` prefix). The handlers return `404` when SSO is disabled, so registering them unconditionally is safe.

### Dependencies

**File:** `server/go.mod` (MODIFY)

```bash
cd server
go get github.com/coreos/go-oidc/v3/oidc
# golang.org/x/oauth2 — promote to direct if currently indirect
go mod tidy
```

---

## Frontend Changes (Web UI)

### 1. Config store: add `ssoEnabled`

**File:** `packages/core/config/index.ts` (MODIFY — `ConfigState` + `setAuthConfig`)

```typescript
interface ConfigState {
  // ... existing fields ...
  ssoEnabled: boolean;
  setAuthConfig: (config: {
    allowSignup: boolean;
    googleClientId?: string;
    workspaceCreationDisabled?: boolean;
    ssoEnabled?: boolean; // NEW
  }) => void;
  // ...
}

// default: false
ssoEnabled: false,
setAuthConfig: ({ allowSignup, googleClientId = "", workspaceCreationDisabled = false, ssoEnabled = false }) =>
  set({ allowSignup, googleClientId, workspaceCreationDisabled, ssoEnabled }),
```

The existing code that calls `GET /api/config` and dispatches `setAuthConfig` must pass `sso_enabled` through. (Find the config-fetch effect — typically in the app bootstrap — and map `config.sso_enabled` into `setAuthConfig`.)

### 2. Login page: render the Keycloak button

**File:** `packages/views/auth/login-page.tsx` (MODIFY — the shared view, per package-boundary rules)

The login UI lives in `packages/views/auth` (business view), **not** in `apps/web/app/(auth)/login/page.tsx` (which is a thin Next.js wrapper). Add SSO awareness:

```tsx
import { useConfigStore } from "@multica/core/config";

// Inside LoginPage:
const ssoEnabled = useConfigStore((s) => s.ssoEnabled);
const searchParams = useSearchParams(); // or however the view receives them
const ssoError = searchParams?.get("error");

// If SSO is enabled, render the Keycloak button (optionally keep the magic-link
// form as a fallback for admins / non-SSO users).
{ssoEnabled && (
  <Button
    onClick={() => {
      const next = sanitizeNextUrl(window.location.pathname + window.location.search);
      window.location.href = `/auth/keycloak/login?next=${encodeURIComponent(next)}`;
    }}
  >
    {t(($) => $.sso.button)} {/* "Login with Keycloak" */}
  </Button>
)}

// Surface SSO errors redirected back from the callback:
{ssoError === "signup_prohibited" && (
  <p className="text-destructive text-sm">
    {t(($) => $.sso.unauthorized)} {/* "Your account is not authorized. Please contact Admin IT." */}
  </p>
)}
{ssoError === "sso_failed" && (
  <p className="text-destructive text-sm">
    {t(($) => $.sso.failed)} {/* "SSO failed, please try again." */}
  </p>
)}
```

> `window.location.href` is used (not Next.js `router.push`) because `/auth/keycloak/login` is a **backend** endpoint that performs an HTTP 302 to Keycloak — it is not a client-side route.

### 3. i18n strings

Multica uses `@multica/views/i18n` with `useT`. Supported locales are `en`, `zh-Hans`, `ko`, `ja` (see `supportedLanguages` in `server/internal/handler/auth.go:47`). Add an `sso` section to the existing `auth` namespace for each locale (do **not** create a separate `sso` namespace unless the existing i18n setup uses per-feature namespaces — follow the established pattern).

Example (`en`):
```json
{
  "sso": {
    "button": "Login with Keycloak",
    "unauthorized": "Your account is not authorized. Please contact Admin IT.",
    "failed": "SSO failed, please try again."
  }
}
```

### 4. No new pages, no new routes

- **No `/sso/callback` page.** After the callback sets the `multica_auth` cookie and redirects to `/` (or `next`), the existing `AuthInitializer` (`packages/core/platform/auth-initializer.tsx`) calls `initialize()` → `getMe()` and the app boots normally.
- **No `/unauthorized` page.** Rejected emails redirect to `/login?error=signup_prohibited`, handled by the login page above.
- **No `POST /v1/auth/sso/exchange` endpoint.** The cookie is set directly by the callback.

---

## Changes Overview

| # | File | Type | Description |
|---|------|------|-------------|
| **Backend — Config** | | | |
| 1 | `server/internal/handler/handler.go` | MODIFY | Add SSO fields to `Config` struct + `OIDC` field to `Handler` struct |
| 2 | `server/cmd/server/router.go` | MODIFY | Read `MULTICA_SSO_*` env vars into `signupConfig`; init `OIDCClient` + assign `h.OIDC` |
| 3 | `server/internal/handler/config.go` | MODIFY | Add `SSOEnabled` to `AppConfig` + return it from `GetConfig` |
| **Backend — SSO Package** | | | |
| 4 | `server/internal/sso/oidc.go` | NEW | OIDC client: discovery, auth URL, token exchange, email extraction |
| 5 | `server/internal/sso/state.go` | NEW | Signed `SameSite=Lax` state cookie (PKCE + state + next) |
| **Backend — Handler** | | | |
| 6 | `server/internal/handler/sso.go` | NEW | `KeycloakLogin` + `KeycloakCallback` on existing `Handler` (reuses `findOrCreateUser`/`issueJWT`/`SetAuthCookies`) |
| **Backend — Routes** | | | |
| 7 | `server/cmd/server/router.go` | MODIFY | Register `GET /auth/keycloak/login` + `GET /auth/keycloak/callback` (public, rate-limited) |
| **Backend — Dependencies** | | | |
| 8 | `server/go.mod` / `server/go.sum` | MODIFY | Add `github.com/coreos/go-oidc/v3/oidc`; promote `golang.org/x/oauth2` to direct |
| **Frontend — Config** | | | |
| 9 | `packages/core/config/index.ts` | MODIFY | Add `ssoEnabled` to `ConfigState` + `setAuthConfig`; wire from `/api/config` |
| **Frontend — Login UI** | | | |
| 10 | `packages/views/auth/login-page.tsx` | MODIFY | Render "Login with Keycloak" button when `ssoEnabled`; surface `?error=` messages |
| **Frontend — i18n** | | | |
| 11 | `packages/views/auth/locales/*` (en, zh-Hans, ko, ja) | MODIFY | Add `sso.button` / `sso.unauthorized` / `sso.failed` strings |

**Total:** 2 new files, 9 modified files. **No database migrations** (no mapping table — reuses the existing `user` table + signup restrictions). **No new frontend pages or routes.**

---

## Edge Cases & Considerations

### 1. SSO Disabled (default)
`MULTICA_SSO_ENABLED` unset/`false` → `h.OIDC == nil`, handlers return `404`, `AppConfig.SSOEnabled` omitted, login page hides the button. Existing magic-link + Google login work unchanged. Zero impact.

### 2. Keycloak unreachable
`NewOIDCClient` fails at startup → `slog.Error` + `os.Exit(1)`. If Keycloak goes down after startup, `ExchangeCode` returns an error → redirect to `/login?error=sso_failed`. Users can still use the fallback login form (if kept).

### 3. Email outside the whitelisted domain (and not an existing user)
`findOrCreateUser` → `checkSignupAllowed` → `ErrSignupProhibited` → redirect to `/login?error=signup_prohibited`. The login page shows "Your account is not authorized. Please contact Admin IT." This is the chosen access model (no `/unauthorized` page, no Talita DB). Applies to both SSO and the existing magic-link/Google flows — the gate is shared.

### 4. State cookie expired / mismatch
`ReadStateCookie` returns an error or `state` differs → redirect to `/login?error=sso_failed`. The user retries by clicking the button again (fresh PKCE + state).

### 5. SameSite cookie interaction
- `multica_sso_state` is `SameSite=Lax` (required for the cross-site OIDC callback).
- `multica_auth` is `SameSite=Strict` (existing). It is only **set** during the callback (allowed) and **read** on the subsequent same-site navigation to `/` (allowed). No conflict.

### 6. Open redirect prevention
`next` is sanitized to relative paths only (must start with `/`, reject absolute URLs), mirroring the existing `sanitizeNextUrl` helper used by the login page.

### 7. Desktop app
Unaffected. Desktop uses daemon tokens / PATs (zero-login model). SSO is web-only. `MULTICA_SSO_ENABLED` is not set in desktop mode.

### 8. API / non-browser clients
Unaffected. SSO routes are browser-oriented (redirect-based). API clients continue to use PATs / JWT / gateway tokens.

### 9. Single logout (future)
Currently `logout()` clears the local cookie but not the Keycloak session. A future `/auth/keycloak/logout` could redirect to Keycloak's `end_session_endpoint` (discovered via OIDC well-known config). Out of scope for this phase.

### 10. Role mapping
Roles are determined by workspace membership (`member.role`), not by SSO. Keycloak groups/roles are not mapped. If needed later, group claims could be read from the ID token.

---

## Security Considerations

| Concern | Mitigation |
|---------|------------|
| **CSRF** | OIDC `state` in a signed `SameSite=Lax` cookie, verified on callback |
| **Code interception** | PKCE (S256) — `code_verifier` per-request, `code_challenge` sent to Keycloak, verified during exchange |
| **State cookie tampering** | HMAC-SHA256 signed with `JWT_SECRET` |
| **State cookie theft (XSS)** | HttpOnly — not accessible via JavaScript |
| **Open redirect** | `next` validated — only relative paths allowed |
| **Token in URL** | No token in URL — the JWT is set as an HttpOnly cookie directly by the callback |
| **Client secret / JWT secret** | Env vars only, never serialized |
| **ID token forgery** | Verified via Keycloak JWKS (OIDC discovery) by `go-oidc` |
| **Email spoofing** | Email comes only from the verified ID token / userinfo endpoint, never from client input |

---

## Keycloak Configuration Guide

### Realm
- Realm: `dev` (issuer `https://larasati.lintasarta.co.id/realms/dev`)

### Client
1. **Client ID:** `task-or`
2. **Client Protocol:** `openid-connect`
3. **Access Type:** `confidential` (requires client secret)
4. **Client Secret:** `oV6mcQShpvBtogbTGgGkuzKZ09Fy1o3X` → `MULTICA_SSO_CLIENT_SECRET`
5. **Valid Redirect URIs:** `{origin}/auth/keycloak/callback` (e.g. `http://localhost:3000/auth/keycloak/callback` for local dev)
6. **Web Origins:** `{origin}`
7. **Standard Flow Enabled:** `ON` (Authorization Code flow)
8. **Direct Access Grants:** `OFF`
9. **Implicit Flow:** `OFF`
10. **PKCE Code Challenge Method:** `S256`

### Mappers (claims)
Ensure `email` is included in the ID token (built-in by default). If absent, add a `User Property` mapper: property `email` → token claim `email`, "Add to ID token" `ON`. `preferred_username` and `name` are built-in.

---

## Post-Implementation Checklist

```bash
# Go checks
cd server
go build ./...
go vet ./...

# Frontend checks
pnpm typecheck
pnpm test

# Manual E2E
# 1. Set MULTICA_SSO_* + ALLOW_SIGNUP=false + ALLOWED_EMAIL_DOMAINS=<your domain> in .env
# 2. Start the stack (make dev)
# 3. Open /login → "Login with Keycloak" button appears
# 4. Click → Keycloak (Larasati) login page
# 5. Login with a whitelisted email → redirected back → cookie set → app loads
# 6. Login with a non-whitelisted email → /login?error=signup_prohibited → message shown
# 7. Set MULTICA_SSO_ENABLED=false → button disappears, magic-link still works
```