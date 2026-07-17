# Phased Development Plan: Keycloak SSO Integration

**Date:** 2026-06-30
**Reference:** `supports_doc/implementation_plan_keycloak_sso.md` (full technical spec)
**Objective:** Break down the SSO implementation into 6 focused phases with clear dependencies, deliverables, and verification per phase.

---

## Phase Dependency Diagram

```
Phase 1 (Config & Deps)
   │
   ▼
Phase 2 (SSO Core Package) ── 3 files, parallel within phase
   │
   ▼
Phase 3 (Backend HTTP & Gateway)
   │
   ▼
Phase 4 (Frontend Pages & Routing) ── i18n + pages + routes, parallel within phase
   │
   ▼
Phase 5 (Frontend Auth Integration)
   │
   ▼
Phase 6 (E2E Testing & Verification)
```

**Rule:** Setiap phase harus selesai + verify sebelum phase berikutnya dimulai. Tidak ada skip.

---

## Phase 1: Config & Dependencies

**Goal:** Tambahkan SSO config struct dan Go dependencies. Foundation untuk semua phase berikutnya.

**Kenapa duluan:** Semua komponen SSO (OIDC client, mapping DB, HTTP handler) butuh config struct untuk baca env vars. Tidak ada logic di phase ini — hanya struct + env wiring.

### Tasks

| # | File | Type | Task |
|---|------|------|------|
| 1.1 | `go.mod` / `go.sum` | MODIFY | `go get github.com/coreos/go-oidc/v3/oidc` + promote `golang.org/x/oauth2` dari indirect ke direct |
| 1.2 | `internal/config/config_sso.go` | NEW | Buat `SSOConfig` struct dengan field: `Enabled`, `KeycloakIssuer`, `ClientID`, `ClientSecret`, `RedirectURL`, `MappingDB*` (5 field Talita DB). Semua `json:"-"` |
| 1.3 | `internal/config/config.go` | MODIFY | Add `SSO SSOConfig \`json:"-"\`` ke `Config` struct (line ~42-60) |
| 1.4 | `internal/config/config_load.go` | MODIFY | Add env var reading di `applyEnvOverrides()` untuk semua SSO + Talita DB env vars |

### Env Vars yang Dibaca

```
GOCLAW_SSO_ENABLED=true
GOCLAW_SSO_KEYCLOAK_ISSUER=https://larasati.lintasarta.co.id/realms/dev
GOCLAW_SSO_CLIENT_ID=la-claw
GOCLAW_SSO_CLIENT_SECRET=<redacted>
GOCLAW_SSO_REDIRECT_URL=https://goclaw.example.com/auth/keycloak/callback
TALITA_DB_POSTGRES_HOST=10.24.117.58
TALITA_DB_POSTGRES_USER=talita
TALITA_DB_POSTGRES_PASSWORD=P4ssword
TALITA_DB_POSTGRES_PORT=5432
TALITA_DB_POSTGRES_DATABASE=talita_db
```

### Verification

```bash
go build ./...                      # Must compile
go build -tags sqliteonly ./...     # Must compile (SSO code no-op in lite)
go vet ./...
```

**Manual check:** Start gateway dengan SSO env vars → tidak ada error. Start tanpa SSO env vars → tidak ada error (SSO disabled by default).

### Deliverable

Config struct siap dipakai. `cfg.SSO.Enabled` returns `true` ketika env vars set. Dependencies terinstall.

---

## Phase 2: SSO Core Package (`internal/sso/`)

**Goal:** Buat 3 file core SSO logic: OIDC client, external DB mapping, dan signed cookie helper.

**Kenapa setelah Phase 1:** Package ini butuh `SSOConfig` fields untuk construct OIDC client dan DB connection.

### Tasks — Bisa Parallel

3 file di phase ini saling independent dan bisa dikerjakan paralel:

#### 2A: `internal/sso/oidc.go` (NEW)

| Aspek | Detail |
|-------|--------|
| **Purpose** | OIDC protocol handler: discovery, auth URL, token exchange, email extraction |
| **Key func** | `NewOIDCClient(ctx, issuer, clientID, clientSecret, redirectURL)` — discovers Keycloak endpoints via `{issuer}/.well-known/openid-configuration` |
| **Key func** | `AuthURL(state, codeVerifier)` — generate Keycloak authorization URL dengan PKCE S256 |
| **Key func** | `ExchangeCode(ctx, code, codeVerifier)` — exchange authorization code → ID token → extract `email` claim |
| **Key func** | `GenerateCodeVerifier()` — random PKCE code_verifier (43-128 chars, base64url) |
| **Key func** | `GenerateState()` — random state untuk CSRF protection |
| **Lib** | `github.com/coreos/go-oidc/v3/oidc` + `golang.org/x/oauth2` |
| **Fallback** | Jika `email` claim tidak ada di ID token, query userinfo endpoint |

#### 2B: `internal/sso/mapping.go` (NEW)

| Aspek | Detail |
|-------|--------|
| **Purpose** | External DB connection ke Talita (10.24.117.58) untuk lookup `user_mapping_goclaw` |
| **Key func** | `NewMappingDB(host, port, user, password, dbname)` — create separate `sql.DB` pool (NOT shared dengan main GoClaw DB) |
| **Key func** | `LookupByEmail(ctx, email)` — `SELECT email, username, token FROM user_mapping_goclaw WHERE email = $1` |
| **Key func** | `Close()` — close connection pool |
| **Error** | `ErrMappingNotFound` — sentinel error ketika email tidak ditemukan |
| **Driver** | `pgx/v5/stdlib` (sama dengan main GoClaw DB, sudah di `go.mod`) |
| **Pool config** | `MaxOpenConns=5`, `MaxIdleConns=2`, `ConnMaxLifetime=30m` — low volume (hanya saat SSO login) |
| **Security** | Parameterized query `$1` — anti SQL injection |

#### 2C: `internal/sso/cookie.go` (NEW)

| Aspek | Detail |
|-------|--------|
| **Purpose** | Signed HttpOnly cookie untuk pass token dari backend callback → frontend exchange |
| **Key func** | `SetSessionCookie(w, payload, signingKey)` — set HMAC-SHA256 signed cookie |
| **Key func** | `ReadSessionCookie(r, signingKey)` — verify + decode cookie, return payload |
| **Key func** | `ClearSessionCookie(w)` — delete cookie (single-use) |
| **Struct** | `SessionPayload{Token, UserID, Exp}` — data di dalam cookie |
| **Cookie name** | `goclaw_sso_session` |
| **TTL** | 30 detik — cukup untuk frontend call exchange endpoint |
| **Attributes** | `HttpOnly: true`, `Secure: true` (false di dev), `SameSite: Lax` |
| **Signing** | HMAC-SHA256 dengan gateway token sebagai key — anti tampering |

### Verification

```bash
go build ./...
go vet ./internal/sso/...
```

**Unit test (recommended):**
- `cookie_test.go` — set + read cookie roundtrip, tampered cookie rejected, expired cookie rejected
- `mapping_test.go` — mock DB, test `LookupByEmail` found + not found

### Deliverable

3 file di `internal/sso/` siap dipanggil oleh HTTP handler. Semua function terdefinisi dan tercompile.

---

## Phase 3: Backend HTTP Handler & Gateway Integration

**Goal:** Hubungkan SSO core package ke HTTP routes. Backend bisa handle full OIDC flow end-to-end.

**Kenapa setelah Phase 2:** HTTP handler butuh `OIDCClient`, `MappingDB`, dan cookie helpers dari Phase 2.

### Tasks — Sequential

| # | File | Type | Task |
|---|------|------|------|
| 3.1 | `internal/http/sso_handler.go` | NEW | Buat `SSOHandler` struct dengan 4 route handlers |
| 3.2 | `internal/gateway/server.go` | MODIFY | Add `ssoHandler` field ke `Server` struct + `SetSSOHandler()` method + register routes di `BuildMux()` |
| 3.3 | `internal/webui/handler.go` | MODIFY | Add `"/auth/"` ke `apiPrefixes` (line 11) — supaya SPA handler tidak intercept SSO routes |
| 3.4 | `cmd/gateway.go` | MODIFY | SSO initialization di startup: create `OIDCClient` + `MappingDB` + `SSOHandler`, pass ke server |

### 4 Routes di `sso_handler.go`

| Route | Method | Auth | Purpose |
|-------|--------|------|---------|
| `/auth/keycloak/login` | GET | Public | Initiate OIDC flow → redirect 302 ke Keycloak |
| `/auth/keycloak/callback` | GET | Public | Handle Keycloak callback → exchange code → query Talita DB → set cookie → redirect ke `/sso/callback` atau `/unauthorized` |
| `/v1/auth/sso/exchange` | POST | Public (cookie) | Exchange SSO cookie → return `{token, user_id}` JSON → clear cookie |
| `/v1/auth/sso/status` | GET | Public | Return `{"enabled": true/false}` — untuk frontend cek SSO status |

### Callback Handler Flow (paling critical)

```
1. Read state cookie → verify state param match (CSRF)
2. Get authorization code dari query params
3. ExchangeCode(code, codeVerifier) → dapat email dari ID token
4. LookupByEmail(email) → query Talita DB
   ├─ Found → SetSessionCookie({token, username}) → redirect /sso/callback
   └─ Not found → redirect /unauthorized
```

### Gateway Startup Wiring (`cmd/gateway.go`)

```go
if cfg.SSO.Enabled {
    oidcClient := sso.NewOIDCClient(ctx, cfg.SSO.KeycloakIssuer, ...)
    mappingDB := sso.NewMappingDB(cfg.SSO.MappingDBHost, ...)
    ssoHandler := httpapi.NewSSOHandler(oidcClient, mappingDB, cfg.Gateway.Token, ...)
    server.SetSSOHandler(ssoHandler)
}
```

### Verification

```bash
go build ./...
go build -tags sqliteonly ./...     # SSO code no-op di lite (GOCLAW_SSO_ENABLED tidak set)
go vet ./...
```

**Manual test dengan curl:**
```bash
# 1. Status endpoint
curl http://localhost:8080/v1/auth/sso/status
# Expected: {"enabled":true}

# 2. Login redirect
curl -v http://localhost:8080/auth/keycloak/login
# Expected: 302 redirect ke https://larasati.lintasarta.co.id/realms/dev/auth?...

# 3. Callback tanpa code → redirect ke /unauthorized
curl -v "http://localhost:8080/auth/keycloak/callback"
# Expected: 302 redirect ke /unauthorized
```

### Deliverable

Backend SSO flow fully functional. 4 routes accessible. Keycloak discovery works. Talita DB query works.

---

## Phase 4: Frontend Pages, Routing & i18n

**Goal:** Buat 2 page baru (`/sso/callback`, `/unauthorized`), daftarkan routes, dan tambahkan i18n strings.

**Kenapa setelah Phase 3:** Frontend pages butuh backend endpoints (`/v1/auth/sso/exchange`) yang dibuat di Phase 3.

### Tasks — Bisa Parallel

#### 4A: i18n Files (3 locale files + index.ts)

| # | File | Type | Task |
|---|------|------|------|
| 4A.1 | `ui/web/src/i18n/locales/en/sso.json` | NEW | English: unauthorized title/message, callback authenticating, login ssoButton |
| 4A.2 | `ui/web/src/i18n/locales/vi/sso.json` | NEW | Vietnamese translations |
| 4A.3 | `ui/web/src/i18n/locales/zh/sso.json` | NEW | Chinese translations |
| 4A.4 | `ui/web/src/i18n/index.ts` | MODIFY | Add `sso` namespace ke i18next config |

**Keys:**
```json
{
  "unauthorized": { "title": "...", "message": "Unauthorized, please contact Admin IT" },
  "callback": { "authenticating": "..." },
  "login": { "ssoButton": "Login with Keycloak" }
}
```

#### 4B: New Pages (2 files)

| # | File | Type | Task |
|---|------|------|------|
| 4B.1 | `ui/web/src/pages/sso/sso-callback-page.tsx` | NEW | Loading page: call `POST /v1/auth/sso/exchange` dengan `credentials: "include"` → simpan ke auth store → navigate ke app |
| 4B.2 | `ui/web/src/pages/sso/unauthorized-page.tsx` | NEW | "Unauthorized, please contact Admin IT" page dengan ShieldAlert icon |

**sso-callback-page key points:**
- `useRef` untuk prevent double-call di React StrictMode
- `credentials: "include"` untuk send HttpOnly cookie
- `h-dvh` untuk mobile viewport
- On failure → redirect ke `/unauthorized`

#### 4C: Route Registration (2 files)

| # | File | Type | Task |
|---|------|------|------|
| 4C.1 | `ui/web/src/lib/routes.ts` | MODIFY | Add `SSO_CALLBACK: "/sso/callback"` dan `UNAUTHORIZED: "/unauthorized"` |
| 4C.2 | `ui/web/src/routes.tsx` | MODIFY | Register 2 routes baru sebagai **public** (no `RequireAuth` wrapper) |

### Verification

```bash
cd ui/web && pnpm build             # No TypeScript errors
```

**Manual check:**
- Navigate ke `/unauthorized` → halaman "Unauthorized, please contact Admin IT" tampil
- Navigate ke `/sso/callback` → loading spinner tampil (lalu redirect karena tidak ada cookie)

### Deliverable

2 page baru + 2 route terdaftar + i18n strings untuk 3 bahasa. Halaman bisa diakses.

---

## Phase 5: Frontend Auth Flow Integration

**Goal:** Hubungkan frontend auth flow ke SSO. User yang belum login di-redirect ke Keycloak. Login page menampilkan tombol "Login with Keycloak".

**Kenapa setelah Phase 4:** `RequireAuth` butuh route `/sso/callback` dan `/unauthorized` yang dibuat di Phase 4.

### Tasks — Sequential

| # | File | Type | Task |
|---|------|------|------|
| 5.1 | `ui/web/src/stores/use-ui-store.ts` | MODIFY | Add `ssoEnabled: boolean` field + `setSsoEnabled()` action |
| 5.2 | `ui/web/src/App.tsx` | MODIFY | Add `useEffect` di root component: fetch `GET /v1/auth/sso/status` → set `ssoEnabled` di UI store |
| 5.3 | `ui/web/src/components/shared/require-auth.tsx` | MODIFY | Ketika `ssoEnabled=true` + user belum auth → `window.location.href = "/auth/keycloak/login?return_to=..."` (bukan redirect ke `/login`) |
| 5.4 | `ui/web/src/pages/login/login-page.tsx` | MODIFY | Ketika `ssoEnabled=true` → tampilkan tombol "Login with Keycloak" (redirect ke `/auth/keycloak/login`). Manual token form sebagai fallback di toggle "Advanced" |

### Key Behavior Changes

**`require-auth.tsx` (line 16-17):**
```
Before: if (!token) → <Navigate to="/login">
After:  if (!token && ssoEnabled) → window.location.href = "/auth/keycloak/login"
        if (!token && !ssoEnabled) → <Navigate to="/login">  (existing behavior)
```

**`login-page.tsx`:**
```
Before: Selalu tampilkan token form + pairing form
After:  if (ssoEnabled) → tampilkan "Login with Keycloak" button
        else → tampilkan token form + pairing form (existing)
```

### Verification

```bash
cd ui/web && pnpm build
cd ui/web && pnpm dev
```

**Manual test:**
1. SSO enabled + belum login → akses `/` → redirect ke Keycloak login page
2. SSO disabled + belum login → akses `/` → redirect ke `/login` (existing behavior)
3. SSO enabled → `/login` page → tampilkan tombol "Login with Keycloak"
4. SSO disabled → `/login` page → tampilkan token form (existing behavior)

### Deliverable

Frontend auth flow terintegrasi dengan SSO. User di-redirect ke Keycloak ketika SSO enabled. Login page adaptif.

---

## Phase 6: E2E Testing & Verification

**Goal:** Verifikasi full SSO flow end-to-end dengan Keycloak + Talita DB yang sebenarnya.

**Kenapa setelah Phase 5:** Semua komponen (backend + frontend) sudah selesai. Phase ini hanya testing.

### Prerequisites

1. Keycloak realm `dev` terkonfigurasi di `https://larasati.lintasarta.co.id/realms/dev`
2. Client `la-claw` terkonfigurasi dengan redirect URI yang benar
3. Table `user_mapping_goclaw` ada di Talita DB dengan test data
4. Semua env vars set di `.env.local`

### Test Scenarios

| # | Scenario | Expected Result |
|---|----------|-----------------|
| 6.1 | SSO disabled (no env vars) | Existing login flow works unchanged. No SSO routes active. |
| 6.2 | SSO enabled, user accesses GoClaw | Redirect ke Keycloak login page |
| 6.3 | User login dengan valid credentials + email ada di mapping | Masuk ke GoClaw dengan token dari mapping table. WS connect berhasil. |
| 6.4 | User login dengan valid credentials + email TIDAK ada di mapping | Redirect ke `/unauthorized` — "Unauthorized, please contact Admin IT" |
| 6.5 | User login dengan invalid credentials | Keycloak menampilkan error (tidak sampai GoClaw) |
| 6.6 | User sudah login (token di localStorage) + akses GoClaw | Langsung masuk aplikasi (tidak redirect ke Keycloak) |
| 6.7 | SSO cookie expired sebelum exchange | Redirect ke `/unauthorized` |
| 6.8 | Keycloak server down | `NewOIDCClient` fail at startup → gateway exit dengan error |
| 6.9 | Talita DB down | `LookupByEmail` error → redirect ke `/unauthorized` |
| 6.10 | Token di mapping table invalid/expired | SSO berhasil, tapi WS connect gagal → auth failure loop (admin harus fix token) |

### Verification Commands

```bash
# Go
go fix ./...
go build ./...
go build -tags sqliteonly ./...
go vet ./...

# Frontend
cd ui/web && pnpm build
cd ui/web && pnpm test

# Integration (jika ada SSO-specific tests)
go test -v -tags integration ./tests/integration/ -run SSO
```

### Deliverable

Semua test scenario pass. SSO flow works end-to-end. Ready for production.

---

## Summary

| Phase | Goal | Files | New | Modified | Can Parallel? | Est. Complexity |
|-------|------|-------|-----|----------|---------------|-----------------|
| 1 | Config & Dependencies | 4 | 1 | 3 | No | Low |
| 2 | SSO Core Package | 3 | 3 | 0 | Yes (3 files) | Medium |
| 3 | Backend HTTP & Gateway | 4 | 1 | 3 | No | Medium |
| 4 | Frontend Pages & i18n | 7 | 5 | 2 | Yes (3 groups) | Low |
| 5 | Frontend Auth Integration | 4 | 0 | 4 | No | Low |
| 6 | E2E Testing | 0 | 0 | 0 | N/A | Low |
| **Total** | | **22** | **10** | **12** | | |

### Key Principles

1. **No skip:** Setiap phase harus selesai + verify sebelum lanjut
2. **Verify per phase:** Setiap phase punya verification step sendiri
3. **Parallel within phase:** Phase 2 dan 4 punya tasks yang bisa dikerjakan paralel
4. **No code changes until plan approved:** Dokumen ini hanya perencanaan
5. **SSO disabled = zero impact:** Jika `GOCLAW_SSO_ENABLED` tidak set, semua SSO code no-op. Existing auth flow tidak terpengaruh

---

## Deployment Strategy

### Kenapa Tidak Deploy Per Phase?

Deploy 6 kali terlalu sering dan tidak efisien. Tapi deploy sekali di akhir (big bang) terlalu berisiko — kalau ada bug, sulit isolate apakah masalahnya di backend atau frontend.

**Solusi: 3 Deployment Batch** — kelompokkan phase berdasarkan build artifact (backend vs frontend) dan go-live trigger.

### Key Insight: SSO Adalah Env-Gated

```
GOCLAW_SSO_ENABLED=false (default)  →  Semua SSO code no-op. Zero impact.
GOCLAW_SSO_ENABLED=true             →  SSO aktif. Backend + frontend harus siap.
```

Selama `GOCLAW_SSO_ENABLED` tidak diset, semua code SSO yang sudah dideploy **tidak melakukan apapun**. Ini memungkinkan kita deploy code SSO dengan aman tanpa mengaktifkannya.

### 3 Deployment Batch

```
┌─────────────────────────────────────────────────────────────────┐
│  BATCH 1: Backend Deploy (Phase 1-3 selesai)                    │
│  ─ Build Go binary (dengan atau tanpa embedui)                  │
│  ─ Deploy ke server                                              │
│  ─ SSO DISABLED (GOCLAW_SSO_ENABLED tidak diset)                │
│  ─ Verify: curl /v1/auth/sso/status → {"enabled":false}        │
│  ─ Existing flow tetap jalan normal                              │
└──────────────────────────┬──────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  BATCH 2: Frontend Deploy (Phase 4-5 selesai)                   │
│  ─ Build frontend: cd ui/web && pnpm build                      │
│  ─ Deploy dist/ ke server (atau rebuild Go binary dengan embed) │
│  ─ SSO MASIH DISABLED                                            │
│  ─ Verify: /unauthorized page accessible, /sso/callback exists  │
│  ─ Existing login flow tetap jalan normal (ssoEnabled=false)    │
└──────────────────────────┬──────────────────────────────────────┘
                           │
                           │  Code deployment selesai. Semua SSO
                           │  code sudah di server tapi tidak aktif.
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  BATCH 3: Go-Live (Phase 6 — E2E Testing)                       │
│  ─ TIDAK ada code deployment. Hanya config change.              │
│  ─ Set env vars di .env.local:                                  │
│      GOCLAW_SSO_ENABLED=true                                     │
│      GOCLAW_SSO_KEYCLOAK_ISSUER=https://larasati.lintasarta...  │
│      GOCLAW_SSO_CLIENT_ID=la-claw                                │
│      GOCLAW_SSO_CLIENT_SECRET=***                                │
│      GOCLAW_SSO_REDIRECT_URL=https://goclaw.../auth/keycloak... │
│      TALITA_DB_POSTGRES_*                                        │
│  ─ Restart gateway: systemctl restart goclaw (atau ./goclaw)    │
│  ─ SSO AKTIF                                                     │
│  ─ Verify: curl /v1/auth/sso/status → {"enabled":true}         │
│  ─ Run E2E test scenarios (10 scenarios di Phase 6)             │
└─────────────────────────────────────────────────────────────────┘
```

### Detail Per Batch

#### Batch 1: Backend Deploy

**Trigger:** Phase 1, 2, 3 selesai + `go build` pass + `go vet` pass

**Yang dideploy:**
- Go binary hasil `go build -o goclaw .` (atau `go build -tags embedui -o goclaw .` jika frontend di-embed)
- Tidak ada perubahan `.env.local` — SSO tetap disabled

**Build commands:**
```bash
# Pull latest code
git pull origin feature/keycloak-sso

# Build backend
go build -o goclaw .

# ATAU build dengan embedded frontend (jika frontend sudah built sebelumnya)
cd ui/web && pnpm build && cd ../..
go build -tags embedui -o goclaw .

# Deploy binary ke server
# (terserah method: scp, rsync, docker, dll)
```

**Verify setelah deploy:**
```bash
# 1. Gateway start tanpa error
./goclaw

# 2. SSO status endpoint (harus false)
curl http://localhost:8080/v1/auth/sso/status
# Expected: {"enabled":false}

# 3. Existing login masih works
# Buka browser → /login → token form tampil normal

# 4. SSO routes tidak aktif (handler nil)
curl -v http://localhost:8080/auth/keycloak/login
# Expected: 404 (route tidak terdaftar karena SSO disabled)
```

**Rollback jika ada masalah:**
```bash
# Rollback ke binary sebelumnya
git checkout <previous-commit>
go build -o goclaw .
# Restart gateway
```

**Risk level: VERY LOW** — SSO code ada di binary tapi tidak dieksekusi. `cfg.SSO.Enabled=false` → `ssoHandler=nil` → tidak ada route SSO terdaftar. Existing behavior 100% unchanged.

---

#### Batch 2: Frontend Deploy

**Trigger:** Phase 4, 5 selesai + `pnpm build` pass

**Yang dideploy:**
- Frontend `dist/` hasil `pnpm build`
- Jika embedded: rebuild Go binary dengan `-tags embedui` (frontend baru di-embed)
- Jika separate: deploy `dist/` ke nginx/static file server

**Build commands:**
```bash
# Pull latest code
git pull origin feature/keycloak-sso

# Build frontend
cd ui/web
pnpm install
pnpm build
cd ../..

# Jika embedded frontend:
go build -tags embedui -o goclaw .

# Deploy
```

**Verify setelah deploy:**
```bash
# 1. Buka browser → /unauthorized
# Expected: halaman "Unauthorized, please contact Admin IT" tampil

# 2. Buka browser → /sso/callback
# Expected: loading spinner tampil, lalu redirect ke /unauthorized (karena tidak ada cookie)

# 3. Buka browser → /login
# Expected: token form tampil normal (ssoEnabled=false, jadi tidak ada tombol Keycloak)

# 4. Buka browser → / (root)
# Expected: redirect ke /login (RequireAuth, ssoEnabled=false → existing behavior)

# 5. SSO status endpoint masih false
curl http://localhost:8080/v1/auth/sso/status
# Expected: {"enabled":false}
```

**Rollback jika ada masalah:**
```bash
# Rollback frontend
git checkout <previous-commit>
cd ui/web && pnpm build && cd ../..
go build -tags embedui -o goclaw .   # jika embedded
# Restart gateway
```

**Risk level: VERY LOW** — `ssoEnabled=false` di UI store (status endpoint return false). `RequireAuth` masih redirect ke `/login`. Login page masih tampilkan token form. Halaman `/sso/callback` dan `/unauthorized` ada tapi tidak di-trigger oleh flow manapun.

---

#### Batch 3: Go-Live (Config Change Only)

**Trigger:** Batch 1 + Batch 2 sudah deploy dan verified. Phase 6 E2E test scenarios siap dijalankan.

**Yang dideploy:**
- **TIDAK ADA code deployment.** Hanya edit `.env.local` + restart gateway.

**Langkah go-live:**
```bash
# 1. Edit .env.local — tambahkan SSO env vars
cat >> .env.local << 'EOF'

# ─── Keycloak SSO Configuration ───
export GOCLAW_SSO_ENABLED=true
export GOCLAW_SSO_KEYCLOAK_ISSUER=https://larasati.lintasarta.co.id/realms/dev
export GOCLAW_SSO_CLIENT_ID=la-claw
export GOCLAW_SSO_CLIENT_SECRET=<isi-client-secret-dari-keycloak>
export GOCLAW_SSO_REDIRECT_URL=https://<goclaw-domain>/auth/keycloak/callback

# ─── External Mapping Database (Talita) ───
export TALITA_DB_POSTGRES_HOST=10.24.117.58
export TALITA_DB_POSTGRES_USER=talita
export TALITA_DB_POSTGRES_PASSWORD=P4ssword
export TALITA_DB_POSTGRES_PORT=5432
export TALITA_DB_POSTGRES_DATABASE=talita_db
EOF

# 2. Source env vars
source .env.local

# 3. Restart gateway
# Jika systemd:
sudo systemctl restart goclaw
# Jika manual:
# Kill process lama, start baru
./goclaw

# 4. Verify SSO aktif
curl http://localhost:8080/v1/auth/sso/status
# Expected: {"enabled":true}

# 5. Verify Keycloak discovery berhasil (cek log gateway)
# Expected log: "sso: Keycloak OIDC enabled" "issuer"="https://larasati.lintasarta.co.id/realms/dev"

# 6. Verify Talita DB connection berhasil (cek log gateway)
# Tidak ada error "sso.mapping_db_init"

# 7. Test login redirect
curl -v http://localhost:8080/auth/keycloak/login
# Expected: 302 redirect ke https://larasati.lintasarta.co.id/realms/dev/auth?...

# 8. Run E2E test scenarios (Phase 6 — 10 scenarios)
```

**Rollback jika ada masalah:**
```bash
# Cukup disable SSO — TIDAK perlu rollback code
# Edit .env.local:
#   GOCLAW_SSO_ENABLED=false  (atau comment out semua SSO env vars)
source .env.local
sudo systemctl restart goclaw

# SSO langsung non-aktif. Existing login flow kembali normal.
# Tidak perlu rebuild binary. Tidak perlu redeploy frontend.
```

**Risk level: MEDIUM** — SSO pertama kali aktif. Bisa terjadi:
- Keycloak unreachable → gateway exit at startup (fix: pastikan Keycloak online)
- Talita DB unreachable → mapping lookup gagal → user redirect ke `/unauthorized` (fix: pastikan DB online)
- Token di mapping table invalid → WS connect gagal (fix: update token di mapping table)
- Redirect URI mismatch di Keycloak client config → callback gagal (fix: update Keycloak client config)

**Mitigasi:** Rollback instan dengan `GOCLAW_SSO_ENABLED=false` + restart. Tidak perlu redeploy code.

---

### Git Branching Strategy

```
main (production)
  │
  ├── dev (development server)
  │     │
  │     └── feature/keycloak-sso ← semua phase dikerjakan di sini
  │
  └── (merge feature/keycloak-sso → dev setelah Batch 1+2 verified)
```

**Workflow:**

| Step | Action | Branch |
|------|--------|--------|
| Development | Implement Phase 1-5 | `feature/keycloak-sso` |
| Batch 1 deploy | Build dari `feature/keycloak-sso`, deploy ke server | `feature/keycloak-sso` |
| Batch 2 deploy | Build dari `feature/keycloak-sso`, deploy ke server | `feature/keycloak-sso` |
| Batch 3 go-live | Set env vars + restart | (no code change) |
| Post go-live | Merge `feature/keycloak-sso` → `dev` → `main` | `dev` → `main` |

**Catatan:** Tidak perlu merge ke `main` sebelum go-live. Code bisa live di server dari feature branch. Merge ke `main` setelah E2E tests pass dan SSO confirmed working.

---

### Deployment Checklist

#### Pre-Deployment (sebelum Batch 1)

- [ ] Keycloak realm `dev` accessible di `https://larasati.lintasarta.co.id/realms/dev`
- [ ] Keycloak client `la-claw` terkonfigurasi dengan:
  - [ ] Client Protocol: `openid-connect`
  - [ ] Access Type: `confidential`
  - [ ] Valid Redirect URIs: `https://<goclaw-domain>/auth/keycloak/callback`
  - [ ] Client Secret sudah dicatat (untuk `GOCLAW_SSO_CLIENT_SECRET`)
- [ ] Table `user_mapping_goclaw` sudah dibuat di Talita DB (`talita_db`)
- [ ] Test data sudah insert ke `user_mapping_goclaw` (minimal 1 row untuk testing)
- [ ] GoClaw gateway berjalan normal di server (existing flow works)

#### Batch 1 (Backend Deploy) Checklist

- [ ] Phase 1, 2, 3 code selesai
- [ ] `go build ./...` pass
- [ ] `go build -tags sqliteonly ./...` pass
- [ ] `go vet ./...` pass
- [ ] Binary dideploy ke server
- [ ] Gateway start tanpa error
- [ ] `curl /v1/auth/sso/status` → `{"enabled":false}`
- [ ] Existing login flow masih works (manual test via browser)
- [ ] `curl /auth/keycloak/login` → 404 (SSO routes tidak aktif)

#### Batch 2 (Frontend Deploy) Checklist

- [ ] Phase 4, 5 code selesai
- [ ] `cd ui/web && pnpm build` pass (no TypeScript errors)
- [ ] Frontend `dist/` dideploy (atau Go binary rebuilt dengan `-tags embedui`)
- [ ] `/unauthorized` page accessible di browser
- [ ] `/sso/callback` page accessible di browser (loading spinner tampil)
- [ ] `/login` page masih tampilkan token form (ssoEnabled=false)
- [ ] `/` (root) masih redirect ke `/login` (existing behavior)

#### Batch 3 (Go-Live) Checklist

- [ ] Batch 1 + Batch 2 verified
- [ ] `.env.local` diupdate dengan semua SSO + Talita env vars
- [ ] `GOCLAW_SSO_ENABLED=true`
- [ ] Gateway restart
- [ ] `curl /v1/auth/sso/status` → `{"enabled":true}`
- [ ] Log menampilkan: `"sso: Keycloak OIDC enabled" "issuer"="https://larasati.lintasarta.co.id/realms/dev"`
- [ ] Tidak ada error "sso.oidc_init" atau "sso.mapping_db_init" di log
- [ ] `curl -v /auth/keycloak/login` → 302 redirect ke Keycloak
- [ ] E2E test scenario 6.1 (SSO disabled) — N/A, SSO enabled
- [ ] E2E test scenario 6.2 (redirect ke Keycloak) — PASS
- [ ] E2E test scenario 6.3 (valid user + mapping found) — PASS
- [ ] E2E test scenario 6.4 (valid user + mapping not found) — PASS → `/unauthorized`
- [ ] E2E test scenario 6.5 (invalid credentials) — Keycloak shows error
- [ ] E2E test scenario 6.6 (already logged in) — PASS → langsung masuk app
- [ ] Merge `feature/keycloak-sso` → `dev` → `main`

#### Rollback Plan

| Scenario | Action | Downtime |
|----------|--------|----------|
| Backend bug setelah Batch 1 | Rollback binary ke previous commit + restart | ~2 menit |
| Frontend bug setelah Batch 2 | Rollback `dist/` ke previous build + restart | ~2 menit |
| SSO tidak works setelah go-live | Set `GOCLAW_SSO_ENABLED=false` + restart | ~30 detik |
| Keycloak down setelah go-live | Set `GOCLAW_SSO_ENABLED=false` + restart | ~30 detik |
| Talita DB down setelah go-live | Set `GOCLAW_SSO_ENABLED=false` + restart | ~30 detik |

**Key advantage:** Rollback go-live (Batch 3) **tidak perlu redeploy code** — cukup toggle env var + restart. User kembali ke login manual dalam <1 menit.
