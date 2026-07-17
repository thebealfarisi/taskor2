# Testing Guide: Batch 3 — Go-Live & E2E Verification

**Date:** 2026-07-02
**Batch:** 3 (Phase 6 — Go-Live)
**Reference:** `supports_doc/development_plan_keycloak_sso_phased.md`
**Objective:** Aktifkan SSO di production dan verifikasi semua 10 E2E test scenarios.

---

## Prasyarat

### A. Batch 1 + Batch 2 Sudah Deploy

- Backend `goclaw-bin` sudah include SSO code (Batch 1)
- Frontend `goclaw-ui.service` sudah include SSO pages (Batch 2)
- SSO saat ini **disabled** — existing login flow normal

### B. Keycloak Sudah Configured

- Realm `dev` accessible di `https://larasati.lintasarta.co.id/realms/dev`
- Client `la-claw` terkonfigurasi dengan:
  - Client Protocol: `openid-connect`
  - Access Type: `confidential`
  - Client Secret sudah dicatat
  - Valid Redirect URIs: `https://la-claw.lintasarta.co.id/auth/keycloak/callback`
  - Web Origins: `https://la-claw.lintasarta.co.id`
  - Standard Flow Enabled: ON
  - Direct Access Grants: OFF
  - Implicit Flow: OFF

**Verifikasi Keycloak ready:**
```bash
# Test discovery endpoint
curl -s https://larasati.lintasarta.co.id/realms/dev/.well-known/openid-configuration | jq .issuer
# Expected: "https://larasati.lintasarta.co.id/realms/dev"

# Test authorization endpoint exists
curl -s https://larasati.lintasarta.co.id/realms/dev/.well-known/openid-configuration | jq .authorization_endpoint
# Expected: "https://larasati.lintasarta.co.id/realms/dev/protocol/openid-connect/auth"
```

### C. Talita DB Sudah Configured

- DB `talita_db` accessible di `10.24.117.58:5432`
- Table `user_mapping_goclaw` sudah dibuat
- Minimal 1 test user sudah di-insert

**Verifikasi Talita DB ready:**
```bash
# Test koneksi
PGPASSWORD=P4ssword psql -h 10.24.117.58 -p 5432 -U talita -d talita_db -c "SELECT * FROM user_mapping_goclaw LIMIT 5;"

# Jika table belum ada, buat:
PGPASSWORD=P4ssword psql -h 10.24.117.58 -p 5432 -U talita -d talita_db << 'EOF'
CREATE TABLE IF NOT EXISTS user_mapping_goclaw (
    email    VARCHAR(255) PRIMARY KEY,
    username VARCHAR(255) NOT NULL,
    token    TEXT NOT NULL
);
EOF

# Insert test user (ganti email + token dengan nilai real)
PGPASSWORD=P4ssword psql -h 10.24.117.58 -p 5432 -U talita -d talita_db << 'EOF'
INSERT INTO user_mapping_goclaw (email, username, token)
VALUES ('test.user@lintasarta.co.id', 'testuser', 'GOCLAW_GATEWAY_TOKEN_VALUE')
ON CONFLICT (email) DO UPDATE SET token = EXCLUDED.token;
EOF
```

> **Penting:** Token di `user_mapping_goclaw` harus berupa GoClaw gateway token (`GOCLAW_GATEWAY_TOKEN` dari `.env`) atau API key yang valid (`goclaw_<32hex>`). Jika token tidak valid, user akan login berhasil via Keycloak tapi WS connect gagal.

### D. Test User di Keycloak

- Minimal 1 user di Keycloak realm `dev` dengan email yang ada di `user_mapping_goclaw`
- Minimal 1 user di Keycloak realm `dev` dengan email yang **TIDAK ada** di `user_mapping_goclaw` (untuk test unauthorized)

---

## Step 1: Go-Live — Aktifkan SSO

### 1.1 Edit .env

```bash
cd /home/goclaw/goclaw
nano .env
```

Tambahkan/aktifkan SSO env vars:

```bash
# ─── Keycloak SSO Configuration ───
GOCLAW_SSO_ENABLED=true
GOCLAW_SSO_KEYCLOAK_ISSUER=https://larasati.lintasarta.co.id/realms/dev
GOCLAW_SSO_CLIENT_ID=la-claw
GOCLAW_SSO_CLIENT_SECRET=<isi-client-secret-dari-keycloak-admin>
GOCLAW_SSO_REDIRECT_URL=https://la-claw.lintasarta.co.id/auth/keycloak/callback

# ─── External Mapping Database (Talita) ───
TALITA_DB_POSTGRES_HOST=10.24.117.58
TALITA_DB_POSTGRES_USER=talita
TALITA_DB_POSTGRES_PASSWORD=P4ssword
TALITA_DB_POSTGRES_PORT=5432
TALITA_DB_POSTGRES_DATABASE=talita_db
```

> **`GOCLAW_SSO_REDIRECT_URL`** harus exact match dengan Valid Redirect URIs di Keycloak client config.

### 1.2 Restart Services

```bash
# Restart backend (re-source .env, SSO init)
sudo systemctl restart goclaw-backend

# Restart frontend (re-fetch sso status → true)
sudo systemctl restart goclaw-ui
```

### 1.3 Verifikasi SSO Init di Log

```bash
sudo journalctl -u goclaw-backend --since "1 min ago" --no-pager | grep sso
```

**Expected:**
```
sso: Keycloak OIDC enabled issuer=https://larasati.lintasarta.co.id/realms/dev
```

**Jika error `sso.oidc_init`:**
```bash
# Keycloak tidak reachable — cek koneksi
curl -v https://larasati.lintasarta.co.id/realms/dev/.well-known/openid-configuration

# Jika timeout → firewall/network issue
# Jika 404 → realm "dev" tidak ada
# Jika SSL error → cek certificate
```

**Jika error `sso.mapping_db_init`:**
```bash
# Talita DB tidak reachable — cek koneksi
PGPASSWORD=P4ssword psql -h 10.24.117.58 -p 5432 -U talita -d talita_db -c "SELECT 1;"

# Jika timeout → firewall/network issue
# Jika auth failed → cek credentials di .env
```

### 1.4 Verifikasi SSO Status Endpoint

```bash
curl http://localhost:18790/v1/auth/sso/status
# Expected: {"enabled":true}
```

### 1.5 Verifikasi Login Redirect

```bash
curl -sS -o /dev/null -w "HTTP %{http_code} → Location: %{redirect_url}\n" http://localhost:18790/auth/keycloak/login
# Expected: HTTP 302 → Location: https://larasati.lintasarta.co.id/realms/dev/protocol/openid-connect/auth?...
```

---

## Step 2: E2E Test Scenarios

Jalankan setiap scenario dan catat hasilnya.

### Scenario 1: SSO Disabled (Rollback Test)

> Test ini dilakukan SETELAH semua scenario lain selesai, untuk verify rollback works.

| Step | Action | Expected |
|------|--------|----------|
| 1 | Set `GOCLAW_SSO_ENABLED=false` di `.env` | — |
| 2 | `sudo systemctl restart goclaw-backend` | — |
| 3 | `sudo systemctl restart goclaw-ui` | — |
| 4 | `curl http://localhost:18790/v1/auth/sso/status` | `404` (route tidak terdaftar) |
| 5 | Buka browser → `https://la-claw.lintasarta.co.id/` | Redirect ke `/login` (existing behavior) |
| 6 | Login dengan token manual | Berhasil, dashboard tampil |
| 7 | Tidak ada log SSO di journalctl | ✅ |

**Result:** ☐ PASS ☐ FAIL

---

### Scenario 2: SSO Enabled — Redirect ke Keycloak

| Step | Action | Expected |
|------|--------|----------|
| 1 | Set `GOCLAW_SSO_ENABLED=true` di `.env` | — |
| 2 | Restart `goclaw-backend` + `goclaw-ui` | — |
| 3 | Buka browser (incognito) → `https://la-claw.lintasarta.co.id/` | Redirect ke Keycloak login page |
| 4 | URL bar menampilkan `https://larasati.lintasarta.co.id/realms/dev/protocol/openid-connect/auth?...` | ✅ |
| 5 | URL mengandung `client_id=la-claw` | ✅ |
| 6 | URL mengandung `code_challenge_method=S256` | ✅ |
| 7 | URL mengandung `redirect_uri=https...la-claw.lintasarta.co.id/auth/keycloak/callback` | ✅ |

**Result:** ☐ PASS ☐ FAIL

---

### Scenario 3: Valid User + Mapping Found → Login Berhasil

> **Prasyarat:** User `test.user@lintasarta.co.id` ada di Keycloak DAN di `user_mapping_goclaw`

| Step | Action | Expected |
|------|--------|----------|
| 1 | Buka browser (incognito) → `https://la-claw.lintasarta.co.id/` | Redirect ke Keycloak |
| 2 | Login dengan valid credentials di Keycloak | Keycloak redirect ke `/auth/keycloak/callback?code=...&state=...` |
| 3 | Backend callback: exchange code → email → query Talita DB | Mapping found |
| 4 | Backend set cookie → redirect ke `/sso/callback` | ✅ |
| 5 | Frontend `/sso/callback`: loading spinner "Authenticating..." | ✅ |
| 6 | Frontend POST `/v1/auth/sso/exchange` → dapat `{token, user_id}` | ✅ |
| 7 | Frontend store credentials → navigate ke `/overview` | ✅ |
| 8 | Dashboard tampil, user logged in | ✅ |
| 9 | WebSocket connected (cek di browser DevTools → Network → WS) | ✅ |
| 10 | Cek log backend: tidak ada error SSO | ✅ |

**Result:** ☐ PASS ☐ FAIL

**Jika FAIL di step 3 (mapping not found):**
```bash
# Cek apakah email ada di Talita DB
PGPASSWORD=P4ssword psql -h 10.24.117.58 -U talita -d talita_db \
  -c "SELECT * FROM user_mapping_goclaw WHERE email = 'test.user@lintasarta.co.id';"

# Cek log backend untuk email yang diterima
sudo journalctl -u goclaw-backend --since "5 min ago" --no-pager | grep -i "sso\|email\|mapping"
```

**Jika FAIL di step 6 (exchange gagal):**
```bash
# Cek apakah cookie ter-set
# Buka DevTools → Application → Cookies → la-claw.lintasarta.co.id
# Harus ada: goclaw_sso_session (HttpOnly)

# Jika cookie tidak ada → Secure cookie butuh HTTPS
# Pastikan akses via https://la-claw.lintasarta.co.id (bukan HTTP)

# Cek log backend
sudo journalctl -u goclaw-backend --since "5 min ago" --no-pager | grep sso
```

**Jika FAIL di step 9 (WS connect gagal):**
```bash
# Token di mapping table mungkin invalid
# Cek token di Talita DB
PGPASSWORD=P4ssword psql -h 10.24.117.58 -U talita -d talita_db \
  -c "SELECT token FROM user_mapping_goclaw WHERE email = 'test.user@lintasarta.co.id';"

# Bandingkan dengan GOCLAW_GATEWAY_TOKEN di .env
grep GOCLAW_GATEWAY_TOKEN .env

# Jika berbeda → update token di Talita DB agar match
```

---

### Scenario 4: Valid User + Mapping NOT Found → Unauthorized

> **Prasyarat:** User `unmapped.user@lintasarta.co.id` ada di Keycloak TAPI TIDAK di `user_mapping_goclaw`

| Step | Action | Expected |
|------|--------|----------|
| 1 | Buka browser (incognito) → `https://la-claw.lintasarta.co.id/` | Redirect ke Keycloak |
| 2 | Login dengan credentials user yang tidak ada di mapping | Keycloak redirect ke callback |
| 3 | Backend callback: exchange code → email → query Talita DB | Mapping NOT found |
| 4 | Backend redirect ke `/unauthorized` | ✅ |
| 5 | Halaman "Unauthorized" tampil dengan icon ShieldAlert | ✅ |
| 6 | Pesan: "Unauthorized, please contact Admin IT" | ✅ |
| 7 | Cek log backend: `sso.mapping_not_found` | ✅ |

**Result:** ☐ PASS ☐ FAIL

---

### Scenario 5: Invalid Credentials → Keycloak Error

| Step | Action | Expected |
|------|--------|----------|
| 1 | Buka browser (incognito) → `https://la-claw.lintasarta.co.id/` | Redirect ke Keycloak |
| 2 | Login dengan password salah | Keycloak tampilkan error "Invalid username or password" |
| 3 | Browser tetap di Keycloak login page (tidak redirect ke GoClaw) | ✅ |
| 4 | Tidak ada log SSO di GoClaw backend (request tidak sampai ke GoClaw) | ✅ |

**Result:** ☐ PASS ☐ FAIL

---

### Scenario 6: Already Logged In → Langsung Masuk App

> **Prasyarat:** User sudah login via SSO (Scenario 3 berhasil), browser masih ada token di localStorage

| Step | Action | Expected |
|------|--------|----------|
| 1 | Tutup tab browser (jangan incognito, atau gunakan tab biasa) | — |
| 2 | Buka tab baru → `https://la-claw.lintasarta.co.id/` | Langsung masuk dashboard (tidak redirect ke Keycloak) |
| 3 | `RequireAuth` mendeteksi token di auth store → tidak redirect | ✅ |
| 4 | Dashboard tampil normal | ✅ |

**Result:** ☐ PASS ☐ FAIL

---

### Scenario 7: SSO Cookie Expired → Unauthorized

> SSO session cookie TTL = 30 detik. Test ini memverifikasi cookie expired ditolak.

| Step | Action | Expected |
|------|--------|----------|
| 1 | Login via Keycloak → redirect ke `/sso/callback` | Loading spinner tampil |
| 2 | **Jangan tunggu** — buka DevTools → Network → throttle ke "Slow 3G" | — |
| 3 | Atau: setelah redirect ke `/sso/callback`, tunggu 35+ detik | — |
| 4 | Frontend POST `/v1/auth/sso/exchange` | Cookie expired → 401 |
| 5 | Frontend tampilkan "Authentication failed. Redirecting..." | ✅ |
| 6 | Redirect ke `/unauthorized` | ✅ |
| 7 | Cek log backend: `sso.exchange_cookie_invalid` | ✅ |

**Alternative test (lebih mudah):**
```bash
# Tunggu 35 detik setelah callback, lalu manual navigate ke /sso/callback
# Buka browser → https://la-claw.lintasarta.co.id/sso/callback
# Expected: loading → "Authentication failed" → redirect ke /unauthorized
```

**Result:** ☐ PASS ☐ FAIL

---

### Scenario 8: Keycloak Server Down → Gateway Exit

> Test ini memverifikasi gateway gagal start jika Keycloak tidak reachable.

| Step | Action | Expected |
|------|--------|----------|
| 1 | Set `GOCLAW_SSO_KEYCLOAK_ISSUER=https://invalid.example.com/realms/dev` di `.env` | — |
| 2 | `sudo systemctl restart goclaw-backend` | — |
| 3 | Cek service status: `sudo systemctl status goclaw-backend` | `failed` (exit code 1) |
| 4 | Cek log: `sudo journalctl -u goclaw-backend --since "1 min ago" --no-pager \| grep sso` | `sso.oidc_init error="oidc discovery failed..."` |
| 5 | Restore correct issuer → restart → service running | ✅ |

**Result:** ☐ PASS ☐ FAIL

---

### Scenario 9: Talita DB Down → Unauthorized

> Test ini memverifikasi user redirect ke `/unauthorized` jika Talita DB tidak reachable.

| Step | Action | Expected |
|------|--------|----------|
| 1 | Set `TALITA_DB_POSTGRES_HOST=10.99.99.99` (IP tidak valid) di `.env` | — |
| 2 | `sudo systemctl restart goclaw-backend` | — |
| 3 | Cek log: gateway start tapi SSO init gagal? | — |

> **Catatan:** Talita DB connection dibuat saat startup. Jika DB down saat startup, gateway akan exit. Jika DB down setelah startup (connection pool timeout), `LookupByEmail` akan error → redirect ke `/unauthorized`.

| Step | Action | Expected |
|------|--------|----------|
| 4 | Jika gateway exit → restore correct host → restart | — |
| 5 | Jika gateway running → login via Keycloak dengan valid user | Redirect ke `/unauthorized` (mapping query gagal) |
| 6 | Cek log: `sso.mapping_not_found` atau `sso.mapping_db_error` | ✅ |
| 7 | Restore correct host → restart | ✅ |

**Result:** ☐ PASS ☐ FAIL

---

### Scenario 10: Invalid Token in Mapping → WS Connect Fail

> Test ini memverifikasi SSO login berhasil tapi WS connect gagal jika token di mapping table invalid.

| Step | Action | Expected |
|------|--------|----------|
| 1 | Update Talita DB: set token ke nilai invalid | — |
```bash
PGPASSWORD=P4ssword psql -h 10.24.117.58 -U talita -d talita_db \
  -c "UPDATE user_mapping_goclaw SET token = 'invalid_token' WHERE email = 'test.user@lintasarta.co.id';"
```
| 2 | Buka browser (incognito) → `https://la-claw.lintasarta.co.id/` | Redirect ke Keycloak |
| 3 | Login dengan valid credentials | Keycloak redirect ke callback |
| 4 | Backend: mapping found (token = 'invalid_token') | Set cookie → redirect `/sso/callback` |
| 5 | Frontend: exchange → dapat token → store → navigate ke `/overview` | ✅ (SSO flow berhasil) |
| 6 | Dashboard tampil, tapi WebSocket connect gagal | WS error di console |
| 7 | User stuck — tidak bisa chat, tidak bisa fetch data | ✅ (token invalid) |
| 8 | Restore valid token | — |
```bash
PGPASSWORD=P4ssword psql -h 10.24.117.58 -U talita -d talita_db \
  -c "UPDATE user_mapping_goclaw SET token = '<VALID_GATEWAY_TOKEN>' WHERE email = 'test.user@lintasarta.co.id';"
```

> **Catatan:** Ini bukan bug SSO — ini expected behavior. SSO hanya menyediakan token dari mapping table. Jika token itu invalid, WS connect gagal. Admin harus memastikan token di mapping table valid.

**Result:** ☐ PASS ☐ FAIL

---

## Step 3: Post Go-Live Verification

Setelah semua scenario pass, lakukan final checks:

### 3.1 Verifikasi SSO Active

```bash
# Status endpoint
curl http://localhost:18790/v1/auth/sso/status
# Expected: {"enabled":true}

# Login redirect
curl -sS -o /dev/null -w "HTTP %{http_code} → %{redirect_url}\n" http://localhost:18790/auth/keycloak/login
# Expected: HTTP 302 → https://larasati.lintasarta.co.id/realms/dev/protocol/openid-connect/auth?...

# Log
sudo journalctl -u goclaw-backend --since "10 min ago" --no-pager | grep sso
# Expected: "sso: Keycloak OIDC enabled" + tidak ada error
```

### 3.2 Verifikasi Existing Login Masih Works (Fallback)

```bash
# Buka browser → https://la-claw.lintasarta.co.id/login
# Expected: tombol "Login with Keycloak" tampil + "Advanced (Manual Token)" collapsible
# Klik "Advanced" → token form tampil
# Login dengan token manual → berhasil
```

> Existing login tetap works sebagai fallback. Admin bisa login dengan gateway token meskipun SSO aktif.

### 3.3 Verifikasi Multiple Users

Jika ada multiple users di `user_mapping_goclaw`, test login dengan beberapa user berbeda:

```bash
# Cek semua mapping
PGPASSWORD=P4ssword psql -h 10.24.117.58 -U talita -d talita_db \
  -c "SELECT email, username FROM user_mapping_goclaw ORDER BY email;"
```

Untuk setiap user:
1. Buka incognito → `https://la-claw.lintasarta.co.id/`
2. Login di Keycloak dengan email user tersebut
3. Expected: dashboard tampil dengan user_id = username dari mapping

---

## Step 4: Merge ke Main (Post Go-Live)

Setelah semua E2E scenarios pass dan SSO confirmed working:

```bash
# Merge feature branch ke dev
git checkout dev
git merge feature/keycloak-sso
git push origin dev

# Merge dev ke main
git checkout main
git merge dev
git push origin main

# Tag release
git tag v3.0.0  # atau version yang sesuai
git push origin v3.0.0
```

---

## Rollback Plan

### Rollback Cepat (SSO disabled, tanpa redeploy code)

```bash
# Edit .env
nano .env
# Ubah: GOCLAW_SSO_ENABLED=true → GOCLAW_SSO_ENABLED=false
# Atau comment out semua GOCLAW_SSO_* dan TALITA_DB_*

# Restart services
sudo systemctl restart goclaw-backend
sudo systemctl restart goclaw-ui

# Verify
curl http://localhost:18790/v1/auth/sso/status
# Expected: 404 (SSO disabled)

# Buka browser → https://la-claw.lintasarta.co.id/
# Expected: redirect ke /login (existing behavior, token form)
```

**Downtime:** < 30 detik (restart service saja, tidak perlu rebuild/redeploy)

### Rollback Scenario Table

| Scenario | Action | Downtime |
|----------|--------|----------|
| SSO tidak works setelah go-live | `GOCLAW_SSO_ENABLED=false` + restart | ~30 detik |
| Keycloak down | `GOCLAW_SSO_ENABLED=false` + restart | ~30 detik |
| Talita DB down | `GOCLAW_SSO_ENABLED=false` + restart | ~30 detik |
| Redirect URI mismatch | Fix Keycloak client config (no GoClaw restart) | 0 detik |
| Token di mapping invalid | Update token di Talita DB (no GoClaw restart) | 0 detik |
| Frontend bug | `git checkout <pre-batch2> -- ui/web/` + restart `goclaw-ui` | ~1 menit |
| Backend bug | `git checkout <pre-batch1> -- .` + `go build -o goclaw-bin .` + restart | ~3 menit |

---

## E2E Test Results Summary

| # | Scenario | Result | Notes |
|---|----------|--------|-------|
| 1 | SSO Disabled (rollback) | ☐ PASS ☐ FAIL | |
| 2 | Redirect ke Keycloak | ☐ PASS ☐ FAIL | |
| 3 | Valid user + mapping found | ☐ PASS ☐ FAIL | |
| 4 | Valid user + mapping not found | ☐ PASS ☐ FAIL | |
| 5 | Invalid credentials | ☐ PASS ☐ FAIL | |
| 6 | Already logged in | ☐ PASS ☐ FAIL | |
| 7 | Cookie expired | ☐ PASS ☐ FAIL | |
| 8 | Keycloak server down | ☐ PASS ☐ FAIL | |
| 9 | Talita DB down | ☐ PASS ☐ FAIL | |
| 10 | Invalid token in mapping | ☐ PASS ☐ FAIL | |

**Go-Live Decision:** ☐ APPROVED (all scenarios pass) ☐ REJECTED (issues found)

---

## Troubleshooting

### Problem: Redirect ke Keycloak tapi "Invalid redirect_uri"

**Penyebab:** `GOCLAW_SSO_REDIRECT_URL` di `.env` tidak match dengan Keycloak client config.

**Fix:**
```bash
# Cek redirect URL di .env
grep GOCLAW_SSO_REDIRECT_URL .env
# Harus: https://la-claw.lintasarta.co.id/auth/keycloak/callback

# Update Keycloak client config:
# Keycloak Admin → Clients → la-claw → Valid Redirect URIs
# Pastikan: https://la-claw.lintasarta.co.id/auth/keycloak/callback
# (exact match, termasuk https:// dan path)
```

### Problem: Login berhasil tapi WS connect gagal

**Penyebab:** Token di `user_mapping_goclaw` tidak valid.

**Fix:**
```bash
# Cek token di mapping
PGPASSWORD=P4ssword psql -h 10.24.117.58 -U talita -d talita_db \
  -c "SELECT email, token FROM user_mapping_goclaw WHERE email = 'test.user@lintasarta.co.id';"

# Cek gateway token
grep GOCLAW_GATEWAY_TOKEN .env

# Update mapping dengan token yang valid
PGPASSWORD=P4ssword psql -h 10.24.117.58 -U talita -d talita_db \
  -c "UPDATE user_mapping_goclaw SET token = '<GOCLAW_GATEWAY_TOKEN_VALUE>' WHERE email = 'test.user@lintasarta.co.id';"
```

### Problem: Cookie tidak ter-set (exchange gagal)

**Penyebab:** SSO cookie `Secure: true` butuh HTTPS. Jika akses via HTTP, cookie tidak ter-set.

**Fix:**
- Pastikan akses via `https://la-claw.lintasarta.co.id` (bukan HTTP)
- Jika testing di localhost tanpa HTTPS: ubah `Secure: false` di `internal/sso/cookie.go` (hanya untuk dev, JANGAN untuk production)

### Problem: "state cookie not found" di log

**Penyebab:** Browser tidak mengirim state cookie saat callback. Biasanya karena:
- Cookie `Secure: true` tapi akses via HTTP
- Browser block third-party cookies
- Cookie sudah expired (TTL 5 menit)

**Fix:**
- Pastikan akses via HTTPS
- Pastikan login → callback terjadi dalam 5 menit
- Cek browser cookie settings (allow first-party cookies)

### Problem: Email claim tidak ada di ID token

**Penyebab:** Keycloak client tidak configured untuk include email claim.

**Fix:**
```
Keycloak Admin → Clients → la-claw → Mappers
  → Add mapper:
    - Name: email
    - Mapper Type: User Property
    - Property: email
    - Token Claim Name: email
    - Add to ID token: ON
    - Add to access token: ON
```

### Problem: User login berhasil tapi user_id salah

**Penyebab:** `username` di `user_mapping_goclaw` tidak match dengan GoClaw user.

**Fix:**
```bash
# Cek username di mapping
PGPASSWORD=P4ssword psql -h 10.24.117.58 -U talita -d talita_db \
  -c "SELECT email, username FROM user_mapping_goclaw;"

# Username harus match dengan GoClaw user_id
# Untuk admin: gunakan "system" atau user_id admin di GoClaw
```

---

## Post Go-Live Monitoring

### Yang perlu dimonitor di log:

```bash
# SSO errors (harus tidak ada setelah stabil)
sudo journalctl -u goclaw-backend --since "1 hour ago" --no-pager | grep "sso\." | grep -v "enabled"

# SSO login success (info)
sudo journalctl -u goclaw-backend --since "1 hour ago" --no-pager | grep "sso: Keycloak OIDC enabled"

# Security warnings
sudo journalctl -u goclaw-backend --since "1 hour ago" --no-pager | grep "security.sso"
```

### Yang perlu dimonitor di Talita DB:

```sql
-- Jumlah user yang sudah mapped
SELECT COUNT(*) FROM user_mapping_goclaw;

-- User yang sering login (jika ada audit log)
-- (tidak ada audit log di SSO code saat ini — future enhancement)
```

### Yang perlu dimonitor di Keycloak:

```
Keycloak Admin → Events
  → Filter by client: la-claw
  → Monitor: LOGIN, LOGIN_ERROR, CODE_TO_TOKEN, CODE_TO_TOKEN_ERROR
```
