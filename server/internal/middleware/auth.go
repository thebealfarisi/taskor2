package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func uuidToString(u pgtype.UUID) string { return util.UUIDToString(u) }

func rejectTemporarilyDisabledUser(w http.ResponseWriter, r *http.Request, userID, email, authPath string) bool {
	if !auth.IsTemporarilyDisabledUser(userID, email) {
		return false
	}
	slog.Warn(
		"auth: temporarily disabled user rejected",
		"path", r.URL.Path,
		"user_id", userID,
		"auth_path", authPath,
	)
	writeError(w, http.StatusForbidden, auth.TemporarilyDisabledUserError)
	return true
}

// Auth middleware validates JWT tokens or Personal Access Tokens.
// Token sources (in priority order):
//  1. Authorization: Bearer <token> header (PAT or JWT)
//  2. multica_auth HttpOnly cookie (JWT) — requires valid CSRF token for state-changing requests
//
// Sets X-User-ID and X-User-Email headers on the request for downstream handlers.
//
// patCache is optional; when non-nil, PAT lookups are cached with a short
// TTL (auth.AuthCacheTTL). On cache hit the middleware skips both the DB
// SELECT and the last_used_at UPDATE — last_used_at is therefore refreshed
// at most once per TTL window per token, not per request.
//
// cloudPAT is optional; when non-nil, tokens with the mcn_ prefix are
// validated by calling the Multica Cloud Fleet service rather than the
// local DB. When nil (Fleet URL unset) mcn_ tokens are rejected at the
// prefix branch — we don't fall through to the mul_ / JWT paths, since
// an mcn_ string is by construction not a valid mul_ PAT or JWT.
func Auth(queries *db.Queries, patCache *auth.PATCache, cloudPAT *auth.CloudPATVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// X-Actor-Source is server-set only — any value supplied by
			// the client is untrusted and discarded before the auth
			// branches run. Only the mat_ branch below re-sets it. This
			// is what prevents a client from sending a normal mul_ PAT
			// plus a forged `X-Actor-Source: member` (or anything else)
			// to convince a downstream handler that its request came
			// from a non-task-token path.
			r.Header.Del(headerXActorSource)

			tokenString, fromCookie := extractToken(r)
			if tokenString == "" {
				slog.Debug("auth: no token found", "path", r.URL.Path)
				http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
				return
			}

			// Cookie-based auth requires CSRF validation for state-changing methods.
			if fromCookie && !auth.ValidateCSRF(r) {
				slog.Debug("auth: CSRF validation failed", "path", r.URL.Path)
				http.Error(w, `{"error":"CSRF validation failed"}`, http.StatusForbidden)
				return
			}

			if !authenticateAuthRequest(w, r, tokenString, queries, patCache, cloudPAT) {
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func authenticateAuthRequest(w http.ResponseWriter, r *http.Request, tokenString string, queries *db.Queries, patCache *auth.PATCache, cloudPAT *auth.CloudPATVerifier) bool {
	switch {
	case strings.HasPrefix(tokenString, "mat_"):
		return authenticateTaskToken(w, r, queries, tokenString)
	case strings.HasPrefix(tokenString, auth.CloudPATPrefix):
		return authenticateCloudPAT(w, r, queries, cloudPAT, tokenString)
	case strings.HasPrefix(tokenString, "mul_"):
		return authenticatePersonalAccessToken(w, r, queries, patCache, tokenString)
	default:
		return authenticateJWTToken(w, r, tokenString)
	}
}

func authenticateTaskToken(w http.ResponseWriter, r *http.Request, queries *db.Queries, tokenString string) bool {
	if queries == nil {
		http.Error(w, `{"error":errMsgInvalidToken}`, http.StatusUnauthorized)
		return false
	}
	hash := auth.HashToken(tokenString)
	tt, err := queries.GetTaskTokenByHash(r.Context(), hash)
	if err != nil {
		slog.Warn("auth: invalid task token", "path", r.URL.Path, "error", err)
		http.Error(w, `{"error":errMsgInvalidToken}`, http.StatusUnauthorized)
		return false
	}
	userID := uuidToString(tt.UserID)
	if rejectTemporarilyDisabledUser(w, r, userID, "", "task_token") {
		return false
	}
	r.Header.Set(headerXUserID, userID)
	r.Header.Set("X-Agent-ID", uuidToString(tt.AgentID))
	r.Header.Set("X-Task-ID", uuidToString(tt.TaskID))
	r.Header.Set(headerXWorkspaceID, uuidToString(tt.WorkspaceID))
	// X-Actor-Source flags the auth path so resolveActor and
	// any owner-only handler can deny without re-querying the
	// token table. The value "task_token" is the only signal
	// this header is allowed to carry — strip anything else a
	// client tried to send.
	r.Header.Set(headerXActorSource, "task_token")
	return true
}

func authenticateCloudPAT(w http.ResponseWriter, r *http.Request, queries *db.Queries, cloudPAT *auth.CloudPATVerifier, tokenString string) bool {
	if cloudPAT == nil {
		slog.Warn("auth: mcn_ token presented but cloud verifier not configured", "path", r.URL.Path)
		http.Error(w, `{"error":errMsgInvalidToken}`, http.StatusUnauthorized)
		return false
	}
	identity, err := cloudPAT.Verify(r.Context(), tokenString, ownerLookupFor(queries))
	if err != nil {
		if errors.Is(err, auth.ErrCloudPATInvalid) {
			slog.Warn("auth: cloud rejected mcn_ token", "path", r.URL.Path, "error", err)
			http.Error(w, `{"error":errMsgInvalidToken}`, http.StatusUnauthorized)
			return false
		}
		// Cloud unreachable / 5xx / decode error. We surface
		// 503 so callers (CLI / daemon) can retry — a 401
		// here would tell them to throw out a valid token.
		slog.Warn("auth: cloud pat verify unavailable", "path", r.URL.Path, "error", err)
		http.Error(w, `{"error":"cloud pat verifier unavailable"}`, http.StatusServiceUnavailable)
		return false
	}
	if rejectTemporarilyDisabledUser(w, r, identity.OwnerID, "", "cloud_pat") {
		return false
	}
	r.Header.Set(headerXUserID, identity.OwnerID)
	// Tag the auth path so account-level guards (e.g.
	// handler.RequireHumanActor on /api/cloud-billing/*)
	// can distinguish a cloud-node machine credential
	// from a human PAT/JWT. Mirrors the mat_ branch's
	// stamp of "task_token" — both are server-set,
	// authoritative, and stripped from any client-
	// supplied value at the top of this middleware. Same
	// rationale as MUL-2600: a machine credential
	// (running agent or running cloud node) must not be
	// treated as the owner having approved an account-
	// level action.
	r.Header.Set(headerXActorSource, "cloud_pat")
	return true
}

func authenticatePersonalAccessToken(w http.ResponseWriter, r *http.Request, queries *db.Queries, patCache *auth.PATCache, tokenString string) bool {
	hash := auth.HashToken(tokenString)

	// Cache hit: TTL has not expired, the token was valid the
	// last time we looked, and nothing has invalidated the
	// entry since. Skip the DB SELECT and the last_used_at
	// UPDATE — last_used_at is bumped once per TTL window.
	if userID, ok := patCache.Get(r.Context(), hash); ok {
		if rejectTemporarilyDisabledUser(w, r, userID, "", "pat_cache") {
			return false
		}
		r.Header.Set(headerXUserID, userID)
		return true
	}

	if queries == nil {
		http.Error(w, `{"error":errMsgInvalidToken}`, http.StatusUnauthorized)
		return false
	}
	pat, err := queries.GetPersonalAccessTokenByHash(r.Context(), hash)
	if err != nil {
		slog.Warn("auth: invalid PAT", "path", r.URL.Path, "error", err)
		http.Error(w, `{"error":errMsgInvalidToken}`, http.StatusUnauthorized)
		return false
	}

	userID := uuidToString(pat.UserID)
	if rejectTemporarilyDisabledUser(w, r, userID, "", "pat") {
		return false
	}
	r.Header.Set(headerXUserID, userID)

	// Clamp cache TTL to the token's remaining lifetime so a
	// PAT expiring in <AuthCacheTTL can't continue passing
	// auth on a cache hit after expires_at.
	var expiresAt time.Time
	if pat.ExpiresAt.Valid {
		expiresAt = pat.ExpiresAt.Time
	}
	patCache.Set(r.Context(), hash, userID, auth.TTLForExpiry(time.Now(), expiresAt))

	// Cache miss = TTL expired (or first use after revoke /
	// process restart). Refresh last_used_at; subsequent hits
	// within the TTL window skip this write entirely.
	go queries.UpdatePersonalAccessTokenLastUsed(context.Background(), pat.ID)

	return true
}

func authenticateJWTToken(w http.ResponseWriter, r *http.Request, tokenString string) bool {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return auth.JWTSecret(), nil
	})
	if err != nil || !token.Valid {
		slog.Warn("auth: invalid token", "path", r.URL.Path, "error", err)
		http.Error(w, `{"error":errMsgInvalidToken}`, http.StatusUnauthorized)
		return false
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		slog.Warn("auth: invalid claims", "path", r.URL.Path)
		http.Error(w, `{"error":"invalid claims"}`, http.StatusUnauthorized)
		return false
	}

	sub, ok := claims["sub"].(string)
	if !ok || strings.TrimSpace(sub) == "" {
		slog.Warn("auth: invalid claims", "path", r.URL.Path)
		http.Error(w, `{"error":"invalid claims"}`, http.StatusUnauthorized)
		return false
	}
	email, _ := claims["email"].(string)
	if rejectTemporarilyDisabledUser(w, r, sub, email, "jwt") {
		return false
	}
	r.Header.Set(headerXUserID, sub)
	if email != "" {
		r.Header.Set("X-User-Email", email)
	}

	return true
}

// extractToken returns the bearer token and whether it came from a cookie.
// Priority: Authorization header > multica_auth cookie.
func extractToken(r *http.Request) (token string, fromCookie bool) {
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString != authHeader {
			return tokenString, false
		}
	}

	if cookie, err := r.Cookie(auth.AuthCookieName); err == nil && cookie.Value != "" {
		return cookie.Value, true
	}

	return "", false
}
