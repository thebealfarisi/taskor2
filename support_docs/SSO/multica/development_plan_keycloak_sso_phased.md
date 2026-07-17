# Batched Development Plan: Keycloak SSO Integration (Multica)

**Date:** 2026-07-17 (revised after alignment audit + batch split)
**Reference:** `support_docs/SSO/multica/implementation_plan_keycloak_sso.md` (full technical spec)
**Objective:** Break the SSO implementation into **5 batch** yang masing-masing independen dan dapat di-verify secara terpisah. Setiap batch memiliki **testing gate** — harus lolos verifikasi sebelum lanjut ke batch berikutnya. Jika ada issue, debugging scope terbatas pada satu batch, bukan seluruh implementasi.

> **Audit-driven changes vs the previous draft:** the old phases assumed a `server/internal/config/` package (does not exist), a Talita mapping DB (removed), a `/sso/callback` page + `/v1/auth/sso/exchange` endpoint (unnecessary — Multica already uses HttpOnly JWT cookies), and a `/unauthorized` page (replaced by `/login?error=signup_prohibited`). All file paths and the flow below now match the actual codebase.

---

## Batch Dependency Diagram

```
Batch 1 (Dependencies + Config)
   │  gate: go build + /api/config returns sso_enabled
   ▼
Batch 2 (SSO Core Package — oidc.go + state.go)
   │  gate: go build + unit test verifier/state
   ▼
Batch 3 (Backend Handler + Routes)
   │  gate: curl /auth/keycloak/login → 302 ke Keycloak + callback flow
   ▼
Batch 4 (Frontend: config flag + login button + i18n)
   │  gate: tombol muncul, klik → full flow, error messages tampil
   ▼
Batch 5 (E2E Testing & Verification)
      gate: seluruh test matrix di testing_guide lulus
```

**Rule:** Setiap batch harus selesai + lolos testing gate sebelum batch berikutnya dimulai. Jika gate gagal, fix di batch tersebut — jangan lanjut.

---

## Batch 1: Dependencies + Config

**Goal:** Tambahkan dependensi OIDC dan field SSO pada config yang sudah ada. **Tidak ada behavior change** — hanya struct field + env reading + satu field baru di `/api/config`. Aman: jika ada bug di batch ini, dampaknya hanya compile error atau config tidak terbaca.

### Tasks

| # | File | Type | Task |
|---|------|------|------|
| 1.1 | `server/go.mod` | MODIFY | `go get github.com/coreos/go-oidc/v3/oidc`; promote `golang.org/x/oauth2` ke direct dependency; `go mod tidy` |
| 1.2 | `server/internal/handler/handler.go` | MODIFY | Tambah field SSO (`SSOEnabled`, `SSOIssuer`, `SSOClientID`, `SSOClientSecret`, `SSORedirectURL`) ke struct `Config` (line ~57). Tambah field `OIDC *sso.OIDCClient` ke struct `Handler` (line ~125). |
| 1.3 | `server/cmd/server/router.go` | MODIFY | Baca env vars `MULTICA_SSO_*` ke dalam literal `signupConfig` (line ~166). Setelah `handler.New(...)`, inisialisasi `sso.NewOIDCClient` dan assign `h.OIDC` jika `SSOEnabled`. |
| 1.4 | `server/internal/handler/config.go` | MODIFY | Tambah field `SSOEnabled bool` ke `AppConfig` (json `sso_enabled,omitempty`); set nilainya dari `os.Getenv("MULTICA_SSO_ENABLED")` di `GetConfig`. |

### Testing Gate 1

```bash
# 1. Compile check
cd server
go build ./...
go vet ./...

# 2. Config endpoint (SSO enabled)
# Set env: MULTICA_SSO_ENABLED=true + issuer/client/secret
# Start server, lalu:
curl -s http://localhost:3000/api/config | python -m json.tool
# Ekspektasi: ada field "sso_enabled": true

# 3. Config endpoint (SSO disabled)
# Unset MULTICA_SSO_ENABLED, restart
curl -s http://localhost:3000/api/config | python -m json.tool
# Ekspektasi: tidak ada field "sso_enabled" (omitempty)

# 4. Startup log
# Ekspektasi: log "sso: keycloak oidc enabled" issuer=...
```

**Lolos jika:** go build/vet bersih, `/api/config` mengembalikan `sso_enabled` saat aktif, server start tanpa panic.

---

## Batch 2: SSO Core Package (`server/internal/sso/`)

**Goal:** Implementasi OIDC client dan state cookie sebagai **pure logic** — belum dipanggil handler mana pun. **Tidak ada `mapping.go`** (tidak pakai Talita; mapping pakai `findOrCreateUser` internal yang sudah ada). Batch ini bisa di-unit-test secara terpisah.

### Tasks

| # | File | Type | Task |
|---|------|------|------|
| 2.1 | `server/internal/sso/oidc.go` | NEW | `OIDCClient` struct + `NewOIDCClient` (discovery via issuer), `AuthURL` (PKCE S256 + state), `ExchangeCode` (tukar code → ID token → email, fallback userinfo), `GenerateCodeVerifier`, `GenerateState`. |
| 2.2 | `server/internal/sso/state.go` | NEW | State cookie `multica_sso_state` (`SameSite=Lax`, HttpOnly, HMAC-SHA256 signed dengan `JWT_SECRET`). `SetStateCookie`, `ReadStateCookie`, `ClearStateCookie`. Payload: `{state, code_verifier, next, exp}`, TTL 5 menit. |
| 2.3 | `server/internal/sso/oidc_test.go` | NEW (opsional) | Unit test: `GenerateCodeVerifier` panjang 43-128 base64url; `GenerateState` unik per call; `NewOIDCClient` gagal graceful dengan issuer invalid. |
| 2.4 | `server/internal/sso/state_test.go` | NEW (opsional) | Unit test: `SetStateCookie` → `ReadStateCookie` round-trip; tampered signature → error; expired cookie → error. |

### Testing Gate 2

```bash
cd server
go build ./...
go vet ./...

# Unit test (jika dibuat)
go test ./internal/sso/... -v

# Manual smoke test (opsional, via Go playground / scratch main):
# - NewOIDCClient(ctx, "https://larasati.lintasarta.co.id/realms/dev", ...)
#   → tidak error, provider.Endpoint() terisi
# - AuthURL("state123", "verifier456") → URL mengandung code_challenge + state
```

**Lolos jika:** go build/vet bersih, unit test lulus, `NewOIDCClient` berhasil discovery ke issuer Larasati.

---

## Batch 3: Backend Handler + Routes

**Goal:** Tambah handler Keycloak ke `Handler` yang sudah ada (reuses `findOrCreateUser`/`issueJWT`/`SetAuthCookies`) dan daftarkan route publik. Setelah batch ini, **full OIDC flow sudah bisa diuji via browser/curl** — meski tombol frontend belum ada, Anda bisa manual hit URL `/auth/keycloak/login`.

### Tasks

| # | File | Type | Task |
|---|------|------|------|
| 3.1 | `server/internal/handler/sso.go` | NEW | `KeycloakLogin` (generate PKCE+state → set state cookie → 302 ke Keycloak) dan `KeycloakCallback` (verify state → exchange code → `findOrCreateUser` → `issueJWT` → `SetAuthCookies` → redirect ke `next`/`/`; pada `ErrSignupProhibited` → `/login?error=signup_prohibited`; error lain → `/login?error=sso_failed`). Handler return 404 jika `SSOEnabled=false`/`h.OIDC==nil`. |
| 3.2 | `server/cmd/server/router.go` | MODIFY | Daftarkan `GET /auth/keycloak/login` dan `GET /auth/keycloak/callback` di grup auth publik (line ~688) dengan rate limiter `authRL` yang sama dengan route auth lain. **Tidak ada endpoint exchange.** |

### Testing Gate 3

```bash
cd server
go build ./...
go vet ./...

# --- SSO ENABLED ---
# Set env lengkap (MULTICA_SSO_* + ALLOW_SIGNUP=false + ALLOWED_EMAIL_DOMAINS)

# 3a. Login redirect
curl -v -s -o /dev/null http://localhost:3000/auth/keycloak/login
# Ekspektasi: 302, Location: https://larasati.lintasarta.co.id/realms/dev/protocol/openid-connect/auth?...
#             URL mengandung client_id=task-or, code_challenge, state

# 3b. Full flow via browser (manual, tanpa tombol frontend):
#     - Buka http://localhost:3000/auth/keycloak/login di browser
#     - Login di Keycloak dengan email di domain whitelist
#     - Ekspektasi: redirect balik → cookie multica_auth ter-set → landing di /
#     - Buka DevTools → Cookies → multica_auth (HttpOnly, SameSite=Strict)

# 3c. Rejected email (di luar domain whitelist):
#     - Login Keycloak dengan email di luar domain
#     - Ekspektasi: redirect ke /login?error=signup_prohibited

# 3d. State mismatch:
#     - curl http://localhost:3000/auth/keycloak/callback?code=fake&state=fake
#     - Ekspektasi: redirect ke /login?error=sso_failed

# --- SSO DISABLED ---
# Unset MULTICA_SSO_ENABLED, restart
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:3000/auth/keycloak/login
# Ekspektasi: 404
```

**Lolos jika:** 302 ke Keycloak saat enabled, 404 saat disabled, full flow via browser sukses (cookie ter-set), email di luar domain → `signup_prohibited`, state mismatch → `sso_failed`.

> **Catatan debugging:** Jika callback gagal, cek log backend untuk:
> - `sso.token_exchange` → masalah koneksi Keycloak / code expired / PKCE mismatch
> - `sso_state_mismatch` → state cookie tidak ter-kirim (SameSite/CORS) atau expired
> - `ErrSignupProhibited` → email tidak match whitelist (cek `ALLOWED_EMAIL_DOMAINS`)

---

## Batch 4: Frontend Config Flag + Login Button + i18n

**Goal:** Frontend tahu SSO aktif (via `/api/config` → `useConfigStore`) dan menampilkan tombol "Login with Keycloak" di halaman login. **Tidak ada halaman `/sso/callback` atau `/unauthorized` baru.** Batch ini murni UI — backend sudah berfungsi penuh dari Batch 3.

### Tasks

| # | File | Type | Task |
|---|------|------|------|
| 4.1 | `packages/core/config/index.ts` | MODIFY | Tambah `ssoEnabled: boolean` ke `ConfigState` (default `false`); tambahkan param `ssoEnabled?` ke `setAuthConfig`. |
| 4.2 | (config-fetch bootstrap) | MODIFY | Tempat yang memanggil `GET /api/config` dan `setAuthConfig` — teruskan `config.sso_enabled` ke `setAuthConfig({ ..., ssoEnabled: config.sso_enabled })`. |
| 4.3 | `packages/views/auth/login-page.tsx` | MODIFY | Baca `ssoEnabled` dari `useConfigStore`. Jika aktif, render tombol "Login with Keycloak" → `window.location.href = "/auth/keycloak/login?next=..."`. Baca `?error=` search param: `signup_prohibited` → pesan "akun tidak terotorisasi, hubungi Admin IT"; `sso_failed` → pesan "SSO gagal, coba lagi". |
| 4.4 | `packages/views/auth/locales/*` (en, zh-Hans, ko, ja) | MODIFY | Tambah key i18n `sso.button`, `sso.unauthorized`, `sso.failed` mengikuti pola namespace yang sudah ada. |

### Testing Gate 4

```bash
pnpm typecheck
pnpm test

# --- SSO ENABLED ---
# Buka http://localhost:3000/login
# 4a. Ekspektasi: tombol "Login with Keycloak" muncul
# 4b. Klik tombol → redirect ke Keycloak (sama seperti Batch 3 manual test)
# 4c. Login dengan email di domain whitelist → landing di app, user ter-load
# 4d. Login dengan email di luar domain → /login?error=signup_prohibited → pesan error tampil
# 4e. Buka /login?error=sso_failed langsung → pesan "SSO gagal" tampil

# --- SSO DISABLED ---
# Unset MULTICA_SSO_ENABLED, restart backend
# 4f. Buka /login → tombol "Login with Keycloak" TIDAK muncul
# 4g. Magic-link form tetap berfungsi normal
```

**Lolos jika:** typecheck/test bersih, tombol muncul/hilang sesuai flag, klik tombol → full flow sukses, error messages tampil untuk `signup_prohibited` dan `sso_failed`.

---

## Batch 5: E2E Testing & Verification

**Goal:** Verifikasi seluruh workflow dengan Keycloak Larasati + domain whitelist. Batch ini **tidak menulis kode** — hanya testing menyeluruh mengikuti `testing_guide_keycloak_sso.md`.

### Test Matrix

| # | Skenario | Email | Ekspektasi |
|---|----------|-------|------------|
| 5.1 | Login sukses (domain whitelist) | `user@lintasarta.co.id` | Cookie ter-set → app terbuka |
| 5.2 | Login ditolak (di luar domain) | `user@gmail.com` | `/login?error=signup_prohibited` |
| 5.3 | Cancel login di Keycloak | (back button) | Kembali ke `/login` tanpa crash |
| 5.4 | State mismatch / CSRF | (tamper state param) | `/login?error=sso_failed` |
| 5.5 | SSO disabled | — | Tombol hilang, magic-link tetap jalan, route 404 |
| 5.6 | User existing (di luar domain) | `old-user@gmail.com` (sudah di DB) | Login sukses (existing user always pass) |
| 5.7 | Pengecualian ALLOWED_EMAILS | `kontraktor@vendor.com` (di ALLOWED_EMAILS) | Login sukses |
| 5.8 | Cookie & SameSite | (post-login DevTools) | `multica_auth` Strict, `multica_sso_state` cleared |
| 5.9 | Logout | (klik logout) | Cookie cleared, kembali ke `/login` |

> Detail tiap skenario lihat `support_docs/SSO/multica/testing_guide_keycloak_sso.md`.

### Environment

```bash
MULTICA_SSO_ENABLED=true
MULTICA_SSO_KEYCLOAK_ISSUER=https://larasati.lintasarta.co.id/realms/dev
MULTICA_SSO_CLIENT_ID=task-or
MULTICA_SSO_CLIENT_SECRET=oV6mcQShpvBtogbTGgGkuzKZ09Fy1o3X
MULTICA_SSO_REDIRECT_URL=http://localhost:3000/auth/keycloak/callback
# Domain whitelist — satu baris mencakup seluruh user di domain tersebut
ALLOW_SIGNUP=false
ALLOWED_EMAIL_DOMAINS=lintasarta.co.id
# (Opsional) ALLOWED_EMAILS=kontraktor@vendor.com — pengecualian per-individu di luar domain
```

**Lolos jika:** seluruh 9 skenario lulus sesuai ekspektasi.

---

## Quick Reference: Batch Summary

| Batch | Scope | Files | Testing Gate | Est. Effort |
|-------|-------|-------|-------------|-------------|
| 1 | Dependencies + Config | 4 MODIFY | go build + `/api/config` | Kecil |
| 2 | SSO Core Package | 2 NEW (+2 test) | go build + unit test | Sedang |
| 3 | Backend Handler + Routes | 1 NEW + 1 MODIFY | curl 302 + browser flow | Sedang |
| 4 | Frontend UI + i18n | 4 MODIFY | typecheck + button visible | Kecil |
| 5 | E2E Testing | 0 (test only) | 9 skenario test matrix | Sedang |

**Total:** 2 NEW + 9 MODIFY (kode), 0 migrasi DB, 0 halaman frontend baru.

---

## Yang TIDAK Perlu Dikerjakan (dan alasannya)

| Item | Alasan |
|------|--------|
| `server/internal/config/config.go` | Tidak ada package config; config adalah `handler.Config` di `handler.go`, dibangun di `router.go` via `os.Getenv`. |
| `server/internal/sso/mapping.go` / Talita DB | Tidak pakai Talita. Mapping pakai `findOrCreateUser` internal + signup restrictions. |
| `server/internal/sso/cookie.go` (session-token cookie) | Tidak perlu ferry token; `multica_auth` HttpOnly cookie di-set langsung callback. Yang ada hanya `state.go` (state cookie OIDC). |
| `POST /v1/auth/sso/exchange` | Tidak ada exchange dance. |
| `apps/web/app/(auth)/sso/callback/page.tsx` | Tidak perlu; AuthInitializer sudah handle boot via `getMe()`. |
| `apps/web/app/unauthorized/page.tsx` | Tidak perlu; penolakan via `/login?error=signup_prohibited`. |
| Migrasi database | Tidak ada tabel mapping baru; pakai tabel `user` yang sudah ada. |
| Desktop app | Desktop pakai daemon token/PAT; SSO web-only. |