# Testing Guide: Batch 2 — Frontend SSO Pages & Auth Integration

**Date:** 2026-07-02
**Batch:** 2 (Phase 4-5)
**Reference:** `supports_doc/development_plan_keycloak_sso_phased.md`
**Objective:** Step-by-step guide untuk deploy dan memverifikasi Batch 2 (frontend) di server.

---

## Prasyarat

### A. Batch 1 Sudah Deploy

- Batch 1 (Phase 1-3) sudah deploy dan verified
- `goclaw-bin` binary sudah include SSO code
- Gateway berjalan normal dengan `goclaw-backend` service

### B. SSO Masih Disabled

- `GOCLAW_SSO_ENABLED` tidak diset (atau `false`) di `.env`
- SSO routes tidak aktif (404)
- Existing login flow masih normal

### C. Node.js / pnpm Terinstall di Server

```bash
node --version   # butuh v18+ (Vite 6 requirement)
pnpm --version   # butuh v8+
```

Jika pnpm belum terinstall:
```bash
npm install -g pnpm
```

---

## File yang Berubah di Batch 2

### File Baru (5)

| # | File | Purpose |
|---|------|---------|
| 1 | `ui/web/src/i18n/locales/en/sso.json` | English i18n strings |
| 2 | `ui/web/src/i18n/locales/vi/sso.json` | Vietnamese i18n strings |
| 3 | `ui/web/src/i18n/locales/zh/sso.json` | Chinese i18n strings |
| 4 | `ui/web/src/pages/sso/sso-callback-page.tsx` | Loading page: exchange cookie → token → navigate |
| 5 | `ui/web/src/pages/sso/unauthorized-page.tsx` | "Unauthorized, contact Admin IT" page |

### File Modified (6)

| # | File | Change |
|---|------|--------|
| 6 | `ui/web/src/i18n/index.ts` | Add `sso` namespace imports + registration |
| 7 | `ui/web/src/lib/routes.ts` | Add `SSO_CALLBACK` + `UNAUTHORIZED` routes |
| 8 | `ui/web/src/routes.tsx` | Register 2 public routes + lazy imports |
| 9 | `ui/web/src/stores/use-ui-store.ts` | Add `ssoEnabled` field + `setSsoEnabled` action |
| 10 | `ui/web/src/App.tsx` | Fetch `/v1/auth/sso/status` on mount → set `ssoEnabled` |
| 11 | `ui/web/src/components/shared/require-auth.tsx` | SSO redirect when `ssoEnabled=true` + not authenticated |
| 12 | `ui/web/src/pages/login/login-page.tsx` | Show "Login with Keycloak" button when `ssoEnabled=true` |

---

## Step 1: Deploy Code ke Server

### 1.1 Pull Latest Code

```bash
cd /home/goclaw/goclaw
git pull origin feature/keycloak-sso
```

### 1.2 Install Frontend Dependencies

```bash
cd ui/web
pnpm install
```

**Expected:** Dependencies terinstall tanpa error.

### 1.3 Restart Frontend Service

Server menggunakan `goclaw-ui.service` yang menjalankan `pnpm dev --host 0.0.0.0` (Vite dev server). Frontend di-serve langsung dari source code — **tidak perlu `pnpm build`**. Cukup restart service untuk pick up code baru.

```bash
# Restart frontend dev server (akan re-load semua source files)
sudo systemctl restart goclaw-ui

# Cek status
sudo systemctl status goclaw-ui
# Expected: active (running)

# Cek log untuk verify Vite start
sudo journalctl -u goclaw-ui --since "1 min ago" --no-pager
# Expected: "VITE vX.X.X  ready in XXXms"
```

**Catatan tentang `goclaw-ui.service`:**
- Service menjalankan `pnpm dev --host 0.0.0.0` (Vite dev server dengan HMR)
- Frontend di-serve dari source code langsung — perubahan code langsung terlihat setelah restart
- Tidak perlu `pnpm build` atau `go build -tags embedui`
- Jika hanya ada perubahan frontend (tanpa backend): cukup restart `goclaw-ui`, tidak perlu restart `goclaw-backend`

---

## Step 2: Verifikasi SSO Disabled (Default Behavior)

### 2.1 Pastikan SSO Masih Disabled

```bash
grep GOCLAW_SSO_ENABLED .env
# Expected: (no output) atau GOCLAW_SSO_ENABLED=false
```

### 2.2 Restart Service

```bash
# Restart backend (jika embedded)
sudo systemctl restart goclaw-backend

# Atau restart UI service (jika separate)
sudo systemctl restart goclaw-ui
```

### 2.3 Test Halaman /unauthorized

Buka browser → `https://la-claw.lintasarta.co.id/unauthorized`

**Expected:** Halaman "Unauthorized" dengan icon ShieldAlert dan pesan "Unauthorized, please contact Admin IT"

> Halaman ini ada tapi tidak di-trigger oleh flow manapun ketika SSO disabled. Hanya bisa diakses langsung via URL.

### 2.4 Test Halaman /sso/callback

Buka browser → `https://la-claw.lintasarta.co.id/sso/callback`

**Expected:**
- Loading spinner dengan teks "Authenticating..." tampil sebentar
- Lalu redirect ke `/unauthorized` (karena tidak ada SSO cookie)

> Ini expected — tanpa cookie dari backend callback, exchange endpoint return 401, frontend redirect ke `/unauthorized`.

### 2.5 Test Login Page Tidak Berubah

Buka browser → `https://la-claw.lintasarta.co.id/login`

**Expected:**
- Token form + Pairing form tampil normal (LoginTabs)
- **TIDAK ada** tombol "Login with Keycloak"
- `ssoEnabled=false` di UI store → login page menampilkan existing behavior

### 2.6 Test Root Redirect Tidak Berubah

Buka browser → `https://la-claw.lintasarta.co.id/`

**Expected:**
- Redirect ke `/login` (existing behavior)
- **TIDAK** redirect ke Keycloak

### 2.7 Test SSO Status Endpoint

```bash
curl http://localhost:18790/v1/auth/sso/status
# Expected: 404 (SSO disabled, route tidak terdaftar)
```

> Frontend akan handle 404 sebagai `ssoEnabled=false` di App.tsx useEffect.

### 2.8 Test Existing Login Masih Works

1. Buka `https://la-claw.lintasarta.co.id/login`
2. Login dengan token seperti biasa
3. **Expected:** Login berhasil, dashboard tampil normal

---

## Step 3: Verifikasi SSO Enabled (Smoke Test)

> **PERINGATAN:** Step ini hanya untuk memverifikasi frontend SSO berfungsi. Setelah test, disable SSO kembali.

### 3.1 Enable SSO di .env

```bash
sudo systemctl stop goclaw-backend

# Edit .env — tambahkan/aktifkan SSO env vars
nano .env
# Pastikan ada:
# GOCLAW_SSO_ENABLED=true
# GOCLAW_SSO_KEYCLOAK_ISSUER=https://larasati.lintasarta.co.id/realms/dev
# GOCLAW_SSO_CLIENT_ID=la-claw
# GOCLAW_SSO_CLIENT_SECRET=<secret>
# GOCLAW_SSO_REDIRECT_URL=https://la-claw.lintasarta.co.id/auth/keycloak/callback
# TALITA_DB_POSTGRES_*=...

# Rebuild binary (SSO code sudah ada, tapi env var baru perlu restart)
go build -o goclaw-bin .

# Start service
sudo systemctl start goclaw-backend

# Restart frontend juga agar Vite re-load (opsional, HMR biasanya auto-detect)
sudo systemctl restart goclaw-ui
```

### 3.2 Verifikasi SSO Status Endpoint

```bash
curl http://localhost:18790/v1/auth/sso/status
# Expected: {"enabled":true}
```

### 3.3 Test Login Page Menampilkan Tombol Keycloak

Buka browser → `https://la-claw.lintasarta.co.id/login`

**Expected:**
- Tombol "Login with Keycloak" tampil sebagai primary button (dengan icon KeyRound)
- Collapsible "Advanced (Manual Token)" di bawahnya
- Klik "Advanced" → memperlihatkan Token form + Pairing form (existing)

### 3.4 Test Root Redirect ke Keycloak

1. Buka browser → `https://la-claw.lintasarta.co.id/`
2. **Expected:** Redirect ke Keycloak login page (`https://larasati.lintasarta.co.id/realms/dev/auth?...`)

> `RequireAuth` mendeteksi `ssoEnabled=true` + tidak ada token → `window.location.href = "/auth/keycloak/login"` → backend redirect ke Keycloak.

### 3.5 Test Full SSO Flow (Jika Keycloak + Talita DB Ready)

> Hanya jika Keycloak realm `dev` + client `la-claw` + Talita DB `user_mapping_goclaw` sudah configured.

1. Buka `https://la-claw.lintasarta.co.id/`
2. Redirect ke Keycloak login page
3. Login dengan valid credentials
4. Keycloak redirect ke `/auth/keycloak/callback?code=...&state=...`
5. Backend exchange code → query Talita DB → set cookie → redirect ke `/sso/callback`
6. Frontend `/sso/callback` → POST `/v1/auth/sso/exchange` → dapat token → store → navigate ke `/overview`
7. **Expected:** Dashboard tampil, user logged in dengan token dari mapping table

### 3.6 Test Unauthorized Flow

1. Login ke Keycloak dengan email yang **TIDAK ada** di `user_mapping_goclaw`
2. **Expected:** Redirect ke `/unauthorized` → halaman "Unauthorized, please contact Admin IT"

### 3.7 Test SSO Cookie Expired

1. Tunggu >30 detik setelah callback redirect ke `/sso/callback`
2. Manual navigate ke `/sso/callback` lagi
3. **Expected:** Loading spinner → "Authentication failed" → redirect ke `/unauthorized`

---

## Step 4: Rollback ke SSO Disabled

Setelah smoke test selesai, disable SSO kembali:

```bash
sudo systemctl stop goclaw-backend

# Edit .env — hapus/comment SSO env vars
nano .env
# Hapus atau comment:
# GOCLAW_SSO_ENABLED=true
# (dan semua GOCLAW_SSO_* / TALITA_DB_*)

# Rebuild binary (tanpa SSO aktif)
go build -o goclaw-bin .

# Start service
sudo systemctl start goclaw-backend

# Restart frontend
sudo systemctl restart goclaw-ui
```

**Verify:**
```bash
# SSO status → 404 (disabled)
curl http://localhost:18790/v1/auth/sso/status
# Expected: 404

# Login page → token form (no Keycloak button)
# Buka browser → /login → harus tampil token form normal

# Root → redirect ke /login (bukan Keycloak)
# Buka browser → / → harus redirect ke /login
```

---

## Checklist Verifikasi

### Batch 2 Deployment Checklist

Copy-paste checklist ini dan centang setiap item:

```
─── Deploy ───
□ 1. Code pulled dari feature/keycloak-sso
□ 2. pnpm install berhasil (no error)
□ 3. goclaw-ui service restart berhasil (Vite dev server running)

─── SSO Disabled (Default) ───
□ 4. GOCLAW_SSO_ENABLED tidak diset di .env
□ 5. goclaw-ui service restart berhasil
□ 6. /unauthorized page accessible → "Unauthorized, please contact Admin IT"
□ 7. /sso/callback → loading spinner → redirect ke /unauthorized (no cookie)
□ 8. /login → token form tampil normal (NO Keycloak button)
□ 9. / (root) → redirect ke /login (existing behavior)
□ 10. curl /v1/auth/sso/status → 404 (SSO disabled)
□ 11. Existing token login → berhasil (manual test via browser)

─── SSO Enabled (Smoke Test — Optional) ───
□ 12. GOCLAW_SSO_ENABLED=true di .env
□ 13. goclaw-backend + goclaw-ui restart berhasil
□ 14. curl /v1/auth/sso/status → {"enabled":true}
□ 15. /login → tombol "Login with Keycloak" tampil
□ 16. /login → "Advanced" toggle → memperlihatkan token form
□ 17. / (root) → redirect ke Keycloak login page
□ 18. Full SSO flow (jika Keycloak+Talita ready) → login berhasil
□ 19. Unauthorized user → redirect ke /unauthorized
□ 20. Cookie expired → /sso/callback → redirect ke /unauthorized

─── Rollback ───
□ 21. SSO env vars dihapus dari .env
□ 22. goclaw-backend + goclaw-ui restart
□ 23. curl /v1/auth/sso/status → 404 (SSO disabled kembali)
□ 24. /login → token form normal (no Keycloak button)
□ 25. Existing login flow masih works
```

---

## Troubleshooting

### Problem: Vite dev server error — "Cannot find module './sso.json'"

```bash
# Pastikan file locale baru ada
ls -la ui/web/src/i18n/locales/en/sso.json
ls -la ui/web/src/i18n/locales/vi/sso.json
ls -la ui/web/src/i18n/locales/zh/sso.json

# Jika tidak ada, pull ulang
git pull origin feature/keycloak-sso

# Restart Vite dev server
sudo systemctl restart goclaw-ui
```

### Problem: Vite dev server error — "Module not found: @/pages/sso/sso-callback-page"

```bash
# Pastikan file page baru ada
ls -la ui/web/src/pages/sso/

# Expected: sso-callback-page.tsx, unauthorized-page.tsx

# Restart Vite dev server
sudo systemctl restart goclaw-ui
```

### Problem: Halaman /unauthorized return 404 (bukan halaman SSO)

**Penyebab:** Vite dev server belum restart, atau cache browser.

**Fix:**
```bash
# Restart frontend dev server
sudo systemctl restart goclaw-ui

# Cek log Vite
sudo journalctl -u goclaw-ui --since "1 min ago" --no-pager

# Clear browser cache atau hard refresh (Ctrl+Shift+R)
```

### Problem: Login page tidak menampilkan tombol Keycloak padahal SSO enabled

**Cek:**
```bash
# 1. SSO status endpoint
curl http://localhost:18790/v1/auth/sso/status
# Expected: {"enabled":true}

# 2. Cek browser console untuk error fetch
# Buka DevTools → Console → harus tidak ada error

# 3. Cek Network tab → GET /v1/auth/sso/status → harus 200
# Buka DevTools → Network → refresh → cari "sso/status"

# 4. Hard refresh browser (Ctrl+Shift+R) — mungkin cache lama
```

### Problem: /sso/callback page stuck di "Authenticating..." (dev mode)

**Penyebab:** React StrictMode double-mount di development mode.

**Fix:** Ini hanya terjadi di development (`pnpm dev`). Di production build (`pnpm build`), StrictMode tidak double-mount. Tidak perlu fix untuk production.

### Problem: Redirect ke Keycloak tapi error "redirect_uri mismatch"

**Penyebab:** `GOCLAW_SSO_REDIRECT_URL` di `.env` tidak match dengan Keycloak client config.

**Fix:**
```bash
# Cek redirect URL di .env
grep GOCLAW_SSO_REDIRECT_URL .env
# Harus: https://la-claw.lintasarta.co.id/auth/keycloak/callback

# Update Keycloak client config:
# Keycloak Admin → Clients → la-claw → Valid Redirect URIs
# Tambahkan: https://la-claw.lintasarta.co.id/auth/keycloak/callback
```

### Problem: Full SSO flow gagal di step exchange

**Cek:**
```bash
# 1. Cek log backend
sudo journalctl -u goclaw-backend --since "5 min ago" --no-pager | grep sso

# 2. Jika "sso.exchange_cookie_invalid" → cookie tidak terbaca
#    Kemungkinan: Secure cookie butuh HTTPS, tapi akses via HTTP
#    Fix: pastikan akses via HTTPS di production

# 3. Jika "sso.mapping_not_found" → email tidak ada di Talita DB
#    Fix: tambahkan email ke user_mapping_goclaw table

# 4. Jika "sso.token_exchange" → Keycloak token exchange gagal
#    Cek: GOCLAW_SSO_CLIENT_SECRET benar?
#    Cek: Keycloak client "la-claw" Access Type = "confidential"
```

---

## Rollback Plan

### Rollback Cepat (SSO disabled, frontend code tetap)

```bash
# Hapus/comment SSO env vars dari .env
nano .env
# (hapus GOCLAW_SSO_ENABLED=true)

# Restart backend
sudo systemctl restart goclaw-backend

# Restart frontend (agar App.tsx re-fetch sso status → false)
sudo systemctl restart goclaw-ui
```

### Rollback Frontend (kembali ke frontend sebelum Batch 2)

```bash
# Rollback code frontend saja
cd /home/goclaw/goclaw
git log --oneline -5  # find commit sebelum Batch 2
git checkout <pre-batch2-commit> -- ui/web/

# Restart Vite dev server (akan re-load code lama)
sudo systemctl restart goclaw-ui

# Backend tidak perlu restart (tidak ada perubahan backend di Batch 2)
```

---

## Post-Deployment Notes

1. **Frontend SSO code sekarang ada di server tapi dormant** — `ssoEnabled=false` karena SSO disabled
2. **Halaman `/unauthorized` dan `/sso/callback` accessible** tapi tidak di-trigger oleh flow manapun
3. **Login page tidak berubah** — `ssoEnabled=false` → tampilkan token form seperti biasa
4. **RequireAuth tidak berubah** — `ssoEnabled=false` → redirect ke `/login` (existing behavior)
5. **Keycloak dan Talita DB belum dibutuhkan** — baru dibutuhkan saat Batch 3 (go-live)

### Yang bisa dilakukan sambil menunggu Batch 3

- Setup Keycloak realm `dev` + client `la-claw` di `https://larasati.lintasarta.co.id`
- Buat table `user_mapping_goclaw` di Talita DB
- Insert test data ke `user_mapping_goclaw`
- Test koneksi dari server GoClaw ke Keycloak dan Talita DB
- Pastikan `GOCLAW_SSO_REDIRECT_URL` di `.env` = `https://la-claw.lintasarta.co.id/auth/keycloak/callback`
- Pastikan Keycloak client `la-claw` Valid Redirect URIs include URL di atas

Lihat `supports_doc/implementation_plan_keycloak_sso.md` → section "Keycloak Configuration Guide" untuk detail setup.
