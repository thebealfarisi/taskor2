# Testing Guide: Batch 1 — Backend SSO Infrastructure

**Date:** 2026-06-30
**Batch:** 1 (Phase 1-3)
**Reference:** `supports_doc/development_plan_keycloak_sso_phased.md`
**Objective:** Step-by-step guide untuk deploy dan memverifikasi Batch 1 di server.

---

## Prasyarat

### A. Akses Server

- SSH access ke server GoClaw
- Go 1.26+ terinstall di server
- Git access ke repository `whiteclawv2`
- GoClaw gateway berjalan normal (existing flow works)

### B. Keycloak (Tidak Wajib untuk Batch 1)

Batch 1 hanya deploy code dengan SSO **disabled**. Keycloak tidak dibutuhkan untuk testing Batch 1. Keycloak baru dibutuhkan saat Batch 3 (go-live).

### C. Talita DB (Tidak Wajib untuk Batch 1)

Sama seperti Keycloak — Talita DB tidak dibutuhkan untuk testing Batch 1 karena SSO disabled.

---

## Step 1: Deploy Code ke Server

### 1.1 Pull Latest Code

```bash
ssh user@goclaw-server
cd /path/to/whiteclawv2
git pull origin feature/keycloak-sso
```

### 1.2 Download Dependencies

```bash
export GOPROXY=https://goproxy.cn
go mod tidy
```

**Expected output:** Tidak ada error. `go.sum` mungkin terupdate dengan hash baru untuk `go-oidc/v3` dan transitive dependencies.

**Jika error:**
```bash
# Jika go-oidc/v3 tidak ditemukan, cek go.mod
grep coreos go.mod
# Expected: github.com/coreos/go-oidc/v3/oidc v3.14.1

# Jika version conflict, coba update ke latest
go get github.com/coreos/go-oidc/v3/oidc@latest
go mod tidy
```

### 1.3 Build Binary

```bash
# PostgreSQL build (standard edition) — binary name harus goclaw-bin (sesuai start-backend.sh)
go build -o goclaw-bin .

# Jika ada error, baca pesan error dan perbaiki
```

**Expected:** Binary `goclaw-bin` terbuat tanpa error.

### 1.4 Build SQLite (Desktop/Lite — Optional Check)

```bash
go build -tags sqliteonly ./...
```

**Expected:** Compile tanpa error. SSO code harus no-op di lite edition karena `GOCLAW_SSO_ENABLED` tidak diset.

**Jika error terkait SSO di SQLite build:**
- SSO package (`internal/sso/`) tidak punya build tag restriction, jadi harus compile di semua build
- Pastikan tidak ada import PostgreSQL-specific di SSO code (tidak seharusnya ada — `pgx/v5/stdlib` compatible dengan semua build)

### 1.5 Vet Check

```bash
go vet ./...
```

**Expected:** Tidak ada warning atau error.

### 1.6 Restart Service

Server menggunakan systemd service `goclaw-backend` dengan wrapper script `start-backend.sh`.

```bash
# Restart service agar binary baru + env vars terbaca
sudo systemctl restart goclaw-backend

# Cek status
sudo systemctl status goclaw-backend
# Expected: active (running)

# Cek log startup
sudo journalctl -u goclaw-backend --since "1 min ago" --no-pager
```

**Catatan tentang `start-backend.sh`:**
- Script sudah `set -a; source .env; set +a` — semua vars di `.env` terbaca
- Binary yang dijalankan adalah `goclaw-bin` (bukan `goclaw`)
- **Setiap kali code di-update:** harus `go build -o goclaw-bin .` sebelum restart service
- **Setiap kali `.env` di-edit:** cukup `sudo systemctl restart goclaw-backend` (script re-source `.env` saat restart)

---

## Step 2: Verifikasi SSO Disabled (Default Behavior)

### 2.1 Start Gateway Tanpa SSO Env Vars

```bash
# Pastikan TIDAK ada SSO env vars di .env
grep -E "GOCLAW_SSO|TALITA_DB" .env
# Expected: (no output) — tidak ada SSO env vars

# Restart service (start-backend.sh akan re-source .env)
sudo systemctl restart goclaw-backend

# Cek status
sudo systemctl status goclaw-backend
# Expected: active (running)

# Cek log untuk verify tidak ada SSO init
sudo journalctl -u goclaw-backend --since "1 min ago" --no-pager
```

**Expected log output:**
```
goclaw gateway starting
...
```

**TIDAK boleh ada log:**
- `sso: Keycloak OIDC enabled` — ini hanya muncul jika SSO enabled
- `sso.oidc_init` — ini hanya muncul jika SSO enabled dan error

### 2.2 Test SSO Status Endpoint

```bash
# Di terminal baru di server
curl http://localhost:18790/v1/auth/sso/status
```

**Expected response:**
```json
{"enabled":false}
```

> Jika port berbeda, sesuaikan. Cek `GOCLAW_PORT` di `.env`.

**Jika response 404:**
- SSO handler tidak terdaftar — ini BENAR untuk SSO disabled
- Tapi status endpoint seharusnya tetap terdaftar... 
- **Tunggu:** Status endpoint ada di `SSOHandler.RegisterRoutes()` yang hanya dipanggil jika `ssoHandler != nil`. Jadi 404 adalah expected behavior ketika SSO disabled.
- **Koreksi:** Ketika SSO disabled, `/v1/auth/sso/status` akan return 404. Ini expected. Frontend akan handle 404 sebagai "SSO disabled".

### 2.3 Test SSO Login Route Tidak Aktif

```bash
curl -v http://localhost:18790/auth/keycloak/login
```

**Expected:** HTTP 404 Not Found

```
*   Trying 127.0.0.1:18790...
* Connected to localhost (127.0.0.1) port 18790 (#0)
> GET /auth/keycloak/login HTTP/1.1
> Host: localhost:18790
>
< HTTP/1.1 404 Not Found
```

> Ini BENAR — SSO routes tidak terdaftar ketika disabled. `ssoHandler` adalah `nil`, jadi `if s.ssoHandler != nil` skip route registration.

### 2.4 Test Existing Login Masih Works

```bash
# Test health endpoint
curl http://localhost:18790/health
# Expected: {"status":"ok",...}

# Test agents endpoint dengan token
curl -H "Authorization: Bearer $GOCLAW_GATEWAY_TOKEN" \
     -H "X-GoClaw-User-Id: admin" \
     http://localhost:18790/v1/agents
# Expected: 200 OK dengan list agents
```

### 2.5 Test WebSocket Connect Masih Works

Buka browser → akses GoClaw web UI → login dengan token seperti biasa.

**Expected:** Login berhasil, dashboard tampil normal, WebSocket connected.

---

## Step 3: Verifikasi SSO Enabled (Smoke Test)

> **PERINGATAN:** Step ini hanya untuk memverifikasi code SSO berfungsi. Jangan leave SSO enabled setelah test ini — Batch 2 (frontend) belum deploy. Setelah test, disable SSO kembali.

### 3.1 Set SSO Env Vars Sementara

```bash
# Stop gateway dulu
sudo systemctl stop goclaw-backend

# Tambahkan SSO env vars ke .env (sementara untuk smoke test)
cat >> .env << 'EOF'

# ─── SSO Smoke Test (HAPUS setelah test) ───
GOCLAW_SSO_ENABLED=true
GOCLAW_SSO_KEYCLOAK_ISSUER=https://larasati.lintasarta.co.id/realms/dev
GOCLAW_SSO_CLIENT_ID=la-claw
GOCLAW_SSO_CLIENT_SECRET=TLIVUcyhUiwph8E6MwjbFYFS533BIGWQ
GOCLAW_SSO_REDIRECT_URL=http://localhost:18790/auth/keycloak/callback

# ─── Talita DB ───
TALITA_DB_POSTGRES_HOST=10.24.117.58
TALITA_DB_POSTGRES_USER=talita
TALITA_DB_POSTGRES_PASSWORD=P4ssword
TALITA_DB_POSTGRES_PORT=5432
TALITA_DB_POSTGRES_DATABASE=talita_db
EOF

# Start gateway
sudo systemctl start goclaw-backend

# Cek log
sudo journalctl -u goclaw-backend --since "1 min ago" --no-pager
```

### 3.2 Verifikasi SSO Init di Log

```bash
sudo journalctl -u goclaw-backend --since "2 min ago" --no-pager | grep sso
```

**Expected log output:**
```
sso: Keycloak OIDC enabled issuer=https://larasati.lintasarta.co.id/realms/dev
```

**Jika error `sso.oidc_init`:**
```
sso.oidc_init error="oidc discovery failed for issuer ..."
```
- Keycloak server tidak reachable dari server GoClaw
- Cek: `curl https://larasati.lintasarta.co.id/realms/dev/.well-known/openid-configuration`
- Jika Keycloak belum siap, skip Step 3 dan lanjut ke Step 4 (rollback)

**Jika error `sso.mapping_db_init`:**
```
sso.mapping_db_init error="failed to ping mapping database..."
```
- Talita DB tidak reachable dari server GoClaw
- Cek: `psql -h 10.24.117.58 -U talita -d talita_db` (install psql jika perlu)
- Jika Talita DB belum siap, skip Step 3 dan lanjut ke Step 4 (rollback)

### 3.3 Test SSO Status Endpoint

```bash
curl http://localhost:18790/v1/auth/sso/status
```

**Expected:**
```json
{"enabled":true}
```

### 3.4 Test SSO Login Redirect

```bash
curl -v http://localhost:18790/auth/keycloak/login 2>&1 | head -20
```

**Expected:** HTTP 302 redirect ke Keycloak

```
< HTTP/1.1 302 Found
< Location: https://larasati.lintasarta.co.id/realms/dev/auth?...
```

> URL di Location header harus mengandung `client_id=la-claw`, `redirect_uri=...`, `state=...`, `code_challenge=...`, `code_challenge_method=S256`

### 3.5 Test SSO Callback Tanpa Code

```bash
curl -v "http://localhost:18790/auth/keycloak/callback" 2>&1 | head -10
```

**Expected:** HTTP 302 redirect ke `/unauthorized`

```
< HTTP/1.1 302 Found
< Location: /unauthorized
```

> `/unauthorized` page belum ada (Batch 2 belum deploy), jadi browser akan dapat 404 atau SPA fallback. Ini expected — kita hanya test backend redirect behavior.

### 3.6 Test SSO Exchange Tanpa Cookie

```bash
curl -v -X POST http://localhost:18790/v1/auth/sso/exchange 2>&1 | head -10
```

**Expected:** HTTP 401 Unauthorized

```
< HTTP/1.1 401 Unauthorized
{"error":"invalid or expired SSO session"}
```

---

## Step 4: Rollback ke SSO Disabled

Setelah smoke test selesai, disable SSO kembali:

```bash
# Stop gateway
sudo systemctl stop goclaw-backend

# Hapus SSO env vars dari .env
# Edit .env, hapus semua baris GOCLAW_SSO_* dan TALITA_DB_*
nano .env
# (hapus block "SSO Smoke Test" dan "Talita DB" yang ditambahkan di Step 3.1)

# Start gateway kembali
sudo systemctl start goclaw-backend

# Verify
sudo journalctl -u goclaw-backend --since "1 min ago" --no-pager | grep sso
# Expected: (no output) — tidak ada log SSO
```

**Verify:**
```bash
curl http://localhost:18790/v1/auth/sso/status
# Expected: 404 (SSO routes tidak aktif)
```

---

## Checklist Verifikasi

### Batch 1 Deployment Checklist

Copy-paste checklist ini dan centang setiap item:

```
□ 1. Code pulled dari feature/keycloak-sso
□ 2. go mod tidy berhasil (no error)
□ 3. go build -o goclaw . berhasil (no error)
□ 4. go build -tags sqliteonly ./... berhasil (no error)
□ 5. go vet ./... berhasil (no warning)

─── SSO Disabled (Default) ───
□ 6. Gateway start tanpa SSO env vars → no error
□ 7. Tidak ada log "sso: Keycloak OIDC enabled"
□ 8. curl /v1/auth/sso/status → 404 (route tidak terdaftar)
□ 9. curl /auth/keycloak/login → 404 (route tidak terdaftar)
□ 10. curl /health → 200 OK
□ 11. curl /v1/agents dengan token → 200 OK
□ 12. Browser login dengan token → berhasil (existing flow)

─── SSO Enabled (Smoke Test — Optional) ───
□ 13. Set SSO env vars → gateway start
□ 14. Log menampilkan "sso: Keycloak OIDC enabled"
□ 15. curl /v1/auth/sso/status → {"enabled":true}
□ 16. curl /auth/keycloak/login → 302 redirect ke Keycloak
□ 17. curl /auth/keycloak/callback (no code) → 302 redirect ke /unauthorized
□ 18. curl -X POST /v1/auth/sso/exchange (no cookie) → 401

─── Rollback ───
□ 19. Unset SSO env vars → gateway restart
□ 20. curl /v1/auth/sso/status → 404 (SSO disabled kembali)
□ 21. Existing login flow masih works
```

---

## Troubleshooting

### Problem: `go mod tidy` error — `go-oidc/v3` not found

```bash
# Cek Go version (butuh 1.26+)
go version

# Manual get
go get github.com/coreos/go-oidc/v3/oidc@v3.14.1
go mod tidy
```

### Problem: Build error — `undefined: sso.OIDCClient`

```bash
# Pastikan file internal/sso/oidc.go ada
ls -la internal/sso/
# Expected: oidc.go, mapping.go, cookie.go

# Jika file ada tapi error, cek import path
grep -r "nextlevelbuilder/goclaw/internal/sso" cmd/ internal/
```

### Problem: Build error — `cannot find package "github.com/coreos/go-oidc/v3/oidc"`

```bash
# Download manual
go get github.com/coreos/go-oidc/v3/oidc
go mod vendor  # jika menggunakan vendor mode
```

### Problem: Gateway exit dengan `sso.oidc_init` error

```
sso.oidc_init error="oidc discovery failed for issuer \"https://larasati.lintasarta.co.id/realms/dev\": ..."
```

**Penyebab:** Keycloak server tidak reachable atau realm `dev` tidak ada.

**Cek:**
```bash
# Test Keycloak discovery URL dari server
curl -v https://larasati.lintasarta.co.id/realms/dev/.well-known/openid-configuration

# Jika timeout → Keycloak tidak reachable (firewall/network)
# Jika 404 → realm "dev" tidak ada di Keycloak
# Jika 200 → cek response JSON, pastikan ada "authorization_endpoint" dan "token_endpoint"
```

**Fix:** Pastikan Keycloak server accessible dari server GoClaw. Jika belum siap, disable SSO untuk Batch 1.

### Problem: Gateway exit dengan `sso.mapping_db_init` error

```
sso.mapping_db_init error="failed to ping mapping database: ..."
```

**Penyebab:** Talita DB (10.24.117.58) tidak reachable atau credentials salah.

**Cek:**
```bash
# Test koneksi ke Talita DB
psql -h 10.24.117.58 -p 5432 -U talita -d talita_db
# Password: P4ssword

# Jika tidak ada psql, test dengan nc
nc -zv 10.24.117.58 5432
# Expected: succeeded

# Cek apakah table user_mapping_goclaw ada
# (setelah login psql):
# \dt user_mapping_goclaw
# SELECT * FROM user_mapping_goclaw LIMIT 1;
```

**Fix:** Pastikan Talita DB accessible dan credentials benar. Jika belum siap, disable SSO untuk Batch 1.

### Problem: `curl /v1/auth/sso/status` return 404 padahal SSO enabled

**Cek:**
```bash
# Pastikan env var benar-benar ada di .env
grep GOCLAW_SSO_ENABLED .env
# Expected: GOCLAW_SSO_ENABLED=true

# Pastikan gateway di-restart SETELAH edit .env
# (env vars dibaca saat startup via EnvironmentFile, bukan runtime)
sudo systemctl restart goclaw-backend

# Cek log apakah SSO init berhasil
sudo journalctl -u goclaw-backend --since "1 min ago" --no-pager | grep sso
```

### Problem: Existing login broken setelah deploy

**Ini tidak seharusnya terjadi.** SSO code adalah no-op ketika disabled. Jika existing login broken:

```bash
# Cek apakah ada perubahan di file yang tidak terkait SSO
git diff HEAD~1 -- internal/http/auth.go internal/gateway/router.go
# Expected: (no output) — tidak ada perubahan di auth/router

# Rollback ke previous commit
sudo systemctl stop goclaw-backend
git stash
git checkout <previous-commit> -- .
go build -o goclaw .
sudo systemctl start goclaw-backend
```

---

## Rollback Plan

### Rollback Cepat (SSO disabled, code tetap)

```bash
# Hapus/comment SSO env vars dari .env
nano .env
# (hapus atau comment baris GOCLAW_SSO_ENABLED=true dan TALITA_DB_*)

# Restart service
sudo systemctl restart goclaw-backend
```

### Rollback Penuh (kembali ke code sebelum Batch 1)

```bash
# Stop gateway
sudo systemctl stop goclaw-backend

# Rollback code
git log --oneline -5  # find commit sebelum Batch 1
git checkout <pre-batch1-commit> -- .
go build -o goclaw .

# Start gateway
sudo systemctl start goclaw-backend

# Verify
curl http://localhost:18790/v1/auth/sso/status
# Expected: 404 (endpoint tidak ada sama sekali)
```

---

## Post-Deployment Notes

1. **SSO code sekarang ada di server tapi dormant** — tidak ada impact ke existing flow
2. **Keycloak dan Talita DB belum dibutuhkan** — baru dibutuhkan saat Batch 3 (go-live)
3. **Frontend belum ada perubahan** — `/unauthorized` dan `/sso/callback` page belum ada (Batch 2)
4. **`go.sum` mungkin berubah** setelah `go mod tidy` — ini normal, commit perubahan go.sum
5. **Env vars SSO belum ditambahkan ke `.env`** — baru ditambahkan saat Batch 3 (go-live)

### Yang bisa dilakukan sambil menunggu Batch 2

- Setup Keycloak realm `dev` + client `la-claw` di `https://larasati.lintasarta.co.id`
- Buat table `user_mapping_goclaw` di Talita DB
- Insert test data ke `user_mapping_goclaw`
- Test koneksi dari server GoClaw ke Keycloak dan Talita DB

Lihat `supports_doc/implementation_plan_keycloak_sso.md` → section "Keycloak Configuration Guide" untuk detail setup.
