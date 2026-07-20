package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/logger"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/sso"
)

// sanitizeNext validates a redirect target to prevent open-redirect attacks.
// Only relative paths starting with "/" are allowed; absolute URLs, protocol-
// relative URLs, and paths with control characters are rejected.
// Mirrors the frontend sanitizeNextUrl in packages/core/auth/utils.ts.
func sanitizeNext(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// Must start with a single "/" (not "//" which is protocol-relative,
	// not "/\" which can bypass some browser checks).
	if !strings.HasPrefix(raw, "/") {
		return ""
	}
	if strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "/\\") {
		return ""
	}
	// Reject control characters (null, tab, CR, LF, etc.).
	for _, ch := range raw {
		if ch < 0x20 || ch == 0x7f {
			return ""
		}
	}
	return raw
}

// KeycloakLogin initiates the OIDC authorization code flow with PKCE.
// GET /auth/keycloak/login?next=/path
func (h *Handler) KeycloakLogin(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.SSOEnabled || h.OIDC == nil {
		http.NotFound(w, r)
		return
	}

	codeVerifier, err := sso.GenerateCodeVerifier()
	if err != nil {
		slog.Error("sso: generate code verifier", "error", err)
		http.Redirect(w, r, "/login?error=sso_failed", http.StatusFound)
		return
	}

	state, err := sso.GenerateState()
	if err != nil {
		slog.Error("sso: generate state", "error", err)
		http.Redirect(w, r, "/login?error=sso_failed", http.StatusFound)
		return
	}

	next := sanitizeNext(r.URL.Query().Get("next"))

	if err := sso.SetStateCookie(w, sso.StatePayload{
		State:        state,
		CodeVerifier: codeVerifier,
		Next:         next,
	}, auth.JWTSecret()); err != nil {
		slog.Error("sso: set state cookie", "error", err)
		http.Redirect(w, r, "/login?error=sso_failed", http.StatusFound)
		return
	}

	authURL := h.OIDC.AuthURL(state, codeVerifier)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// KeycloakCallback handles the OIDC callback from Keycloak.
// GET /auth/keycloak/callback?code=...&state=...
func (h *Handler) KeycloakCallback(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.SSOEnabled || h.OIDC == nil {
		http.NotFound(w, r)
		return
	}

	// 1. Verify state cookie (CSRF protection).
	sp, err := sso.ReadStateCookie(r, auth.JWTSecret())
	if err != nil || sp.State != r.URL.Query().Get("state") {
		slog.Warn("sso: state mismatch", "error", err)
		sso.ClearStateCookie(w)
		http.Redirect(w, r, "/login?error=sso_failed", http.StatusFound)
		return
	}

	// 2. Get authorization code.
	code := r.URL.Query().Get("code")
	if code == "" {
		sso.ClearStateCookie(w)
		http.Redirect(w, r, "/login?error=sso_failed", http.StatusFound)
		return
	}

	// 3. Exchange code for ID token, extract email (PKCE verified).
	email, err := h.OIDC.ExchangeCode(r.Context(), code, sp.CodeVerifier)
	if err != nil {
		slog.Error("sso: token exchange", "error", err)
		sso.ClearStateCookie(w)
		http.Redirect(w, r, "/login?error=sso_failed", http.StatusFound)
		return
	}

	// 4. Map to Multica user (reuses the existing signup gate).
	user, isNew, err := h.findOrCreateUser(r.Context(), strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		sso.ClearStateCookie(w)
		var signupErr SignupError
		if errors.As(err, &signupErr) {
			slog.Warn("sso: signup prohibited", "email", email)
			http.Redirect(w, r, "/login?error=signup_prohibited", http.StatusFound)
			return
		}
		slog.Error("sso: find or create user", "error", err, "email", email)
		http.Redirect(w, r, "/login?error=sso_failed", http.StatusFound)
		return
	}

	// 5. Issue JWT + set the existing HttpOnly auth cookies.
	tokenString, err := h.issueJWT(user)
	if err != nil {
		slog.Warn("sso: issue jwt", append(logger.RequestAttrs(r), "error", err, "email", email)...)
		sso.ClearStateCookie(w)
		http.Redirect(w, r, "/login?error=sso_failed", http.StatusFound)
		return
	}

	if err := auth.SetAuthCookies(w, tokenString); err != nil {
		slog.Warn("sso: set auth cookies", "error", err)
	}

	// Set CloudFront signed cookies (same as VerifyCode / GoogleLogin).
	if h.CFSigner != nil {
		for _, cookie := range h.CFSigner.SignedCookies(time.Now().Add(auth.AuthTokenTTL())) {
			http.SetCookie(w, cookie)
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

	// 6. Redirect to the originally requested page (sanitized) or /.
	dest := sp.Next
	if dest == "" {
		dest = "/"
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// KeycloakLogout clears Multica auth cookies and redirects to the Keycloak
// end_session_endpoint to terminate the Keycloak session (Single Logout).
// After Keycloak destroys its session, it redirects back to the Multica
// login page (via post_logout_redirect_uri).
// GET /auth/keycloak/logout
func (h *Handler) KeycloakLogout(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.SSOEnabled || h.OIDC == nil {
		http.NotFound(w, r)
		return
	}

	// 1. Clear Multica auth cookies first (same as regular logout).
	auth.ClearAuthCookies(w)

	// 2. Build post-logout redirect URI (back to the Multica login page).
	// Uses FRONTEND_ORIGIN env var to construct an absolute URL — Keycloak
	// requires an absolute URI for post_logout_redirect_uri.
	appURL := strings.TrimRight(strings.TrimSpace(os.Getenv("FRONTEND_ORIGIN")), "/")
	if appURL == "" {
		// Fallback: relative redirect to /login (Keycloak may reject this
		// if it requires an absolute URI, but better than crashing).
		slog.Warn("sso: FRONTEND_ORIGIN not set; post_logout_redirect_uri will be relative")
		appURL = ""
	}
	postLogoutRedirectURI := appURL + "/login"

	// 3. Get Keycloak end_session_endpoint URL.
	logoutURL, err := h.OIDC.LogoutURL(postLogoutRedirectURI)
	if err != nil {
		slog.Error("sso: build logout url", "error", err)
		// Fallback: redirect to login page directly (Multica cookies already cleared).
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	slog.Info("user logging out via keycloak", logger.RequestAttrs(r)...)
	http.Redirect(w, r, logoutURL, http.StatusFound)
}