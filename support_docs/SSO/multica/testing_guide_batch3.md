# Testing Guide — Batch 3: Backend Handler + Routes

**Date:** 2026-07-17
**Reference:** `support_docs/SSO/multica/development_plan_keycloak_sso_phased.md` (Batch 3)
**Scope:** Verifikasi bahwa route `GET /auth/keycloak/login` dan `GET /auth/keycloak/callback` terdaftar, login redirect ke Keycloak berhasil, dan callback flow (token exchange → user mapping → cookie set → redirect) bekerja end-to-end via browser/curl. **Tombol frontend belum ada** — itu Batch 4.

---

## Environment

| Aktivitas | Lokasi | Shell |
|-----------|--------|-------|
| **Development** (edit kode, commit, push) | Windows: `D:\Kerjaan\Project\taskor2` | PowerShell |
| **Testing** (build, run, verify) | Ubuntu server: `/home/multica/multica` | Bash |

### Deployment Topology (server Ubuntu)

- **Backend:** systemd service `multica-backend`
  - `ExecStart=/home/multica/multica/server/bin/server` (binary)
  - `EnvironmentFile=/home/multica/multica/.env`
  - Restart: `sudo systemctl restart multica-backend`
- **Frontend:** systemd service `multica-frontend`
- **Project root:** `/home/multica/multica`
- **Server module:** `/home/multica/multica/server`

---

## Perubahan yang Diuji

| File | Perubahan |
|------|-----------|
| `server/internal/handler/sso.go` | NEW — `KeycloakLogin` (PKCE + state cookie → 302 ke Keycloak) + `KeycloakCallback` (verify state → exchange code → `findOrCreateUser` → `issueJWT` → `SetAuthCookies` → redirect) + `sanitizeNext` (open-redirect prevention) |
| `server/cmd/server/router.go` | MODIFY — register `GET /auth/keycloak/login` + `GET /auth/keycloak/callback` di public auth group dengan rate limiter |

---

## Persiapan: Pull + Rebuild

```bash
cd /home/multica/multica
git pull origin dev_taskor
cd server
go build -o bin/server ./cmd/server
sudo systemctl restart multica-backend
sleep 2
```

### Pastikan `.env` punya SSO config lengkap:

```bash
grep -E "MULTICA_SSO|ALLOW_SIGNUP|ALLOWED_EMAIL" /home/multica/multica/.env
```

**Ekspektasi minimal:**
```
MULTICA_SSO_ENABLED=true
MULTICA_SSO_KEYCLOAK_ISSUER=https://larasati.lintasarta.co.id/realms/dev
MULTICA_SSO_CLIENT_ID=task-or
MULTICA_SSO_CLIENT_SECRET=oV6mcQShpvBtogbTGgGkuzKZ09Fy1o3X
MULTICA_SSO_REDIRECT_URL=http://localhost:3000/auth/keycloak/callback
MULTICA_SSO_SKIP_TLS_VERIFY=true
ALLOW_SIGNUP=false
ALLOWED_EMAIL_DOMAINS=lintasarta.co.id
```

> **Penting:** `MULTICA_SSO_REDIRECT_URL` harus sesuai dengan origin yang Anda akses dari browser. Jika server ada di `http://latbsdbtltprd01:3000`, set `MULTICA_SSO_REDIRECT_URL=http://latbsdbtltprd01:3000/auth/keycloak/callback`. Juga harus terdaftar sebagai **Valid Redirect URI** di Keycloak client `task-or`.

---

## Skenario Uji

### T1. Compile Check (wajib)

```bash
cd /home/multica/multica/server
go build ./...
go vet ./...
```

**Ekspektasi:** tidak ada output (sukses).

---

### T2. Login Redirect — SSO Enabled (wajib)

**Tujuan:** Pastikan `GET /auth/keycloak/login` mengembalikan 302 redirect ke Keycloak authorization endpoint.

```bash
curl -v -s -o /dev/null http://localhost:3000/auth/keycloak/login 2>&1 | grep -E "HTTP/|Location|code_challenge|client_id|state"
```

**Ekspektasi:**
```
HTTP/1.1 302 Found
Location: https://larasati.lintasarta.co.id/realms/dev/protocol/openid-connect/auth?client_id=task-or&code_challenge=...&code_challenge_method=S256&response_type=code&scope=openid+profile+email&state=...&redirect_uri=...
```

**Verifikasi:**
- Status code `302`
- `Location` header mengandung `client_id=task-or`
- `Location` mengandung `code_challenge` (PKCE)
- `Location` mengandung `state` (CSRF)
- `Location` mengandung `redirect_uri=http://localhost:3000/auth/keycloak/callback`

**Gagal jika:**
- `404` → SSO disabled atau binary belum di-rebuild
- `302` ke `/login?error=sso_failed` → gagal generate PKCE/state atau set state cookie

---

### T3. Login Redirect — SSO Disabled (wajib)

**Tujuan:** Pastikan route return 404 saat SSO dimatikan.

```bash
# Edit .env: MULTICA_SSO_ENABLED=false
sed -i 's/MULTICA_SSO_ENABLED=true/MULTICA_SSO_ENABLED=false/' /home/multica/multica/.env
sudo systemctl restart multica-backend
sleep 2

curl -s -o /dev/null -w "%{http_code}\n" http://localhost:3000/auth/keycloak/login
```

**Ekspektasi:** `404`

**Kembalikan ke enabled setelah test:**
```bash
sed -i 's/MULTICA_SSO_ENABLED=false/MULTICA_SSO_ENABLED=true/' /home/multica/multica/.env
sudo systemctl restart multica-backend
sleep 2
```

---

### T4. Full Flow via Browser — Email di Domain Whitelist (wajib)

**Tujuan:** Verifikasi end-to-end flow dari login → Keycloak → callback → cookie set → landing di app.

**Langkah:**
1. Buka browser, akses `http://localhost:3000/auth/keycloak/login` (atau `http://<server-host>:3000/auth/keycloak/login`)
2. Browser redirect ke halaman login Keycloak Larasati
3. Login dengan username + password Keycloak untuk user yang emailnya **ADA di `ALLOWED_EMAIL_DOMAINS`** (mis. `user@lintasarta.co.id`)
4. Keycloak redirect balik ke `/auth/keycloak/callback?code=...&state=...`
5. Backend verify state, exchange code, map user, set cookie, redirect ke `/`

**Ekspektasi:**
- Browser landing di `/` (halaman utama Multica)
- User ter-authenticate (bisa akses dashboard/workspace)
- DevTools → Application → Cookies → `multica_auth` (HttpOnly, SameSite=Strict) ter-set
- DevTools → Cookies → `multica_sso_state` sudah ter-clear (single-use)

**Gagal jika:**
- Redirect ke `/login?error=sso_failed` → cek log backend:
  ```bash
  sudo journalctl -u multica-backend --since "2 min ago" | grep "sso:"
  ```
  Kemungkinan: state mismatch, token exchange gagal, Keycloak unreachable
- Redirect ke `/login?error=signup_prohibited` → email tidak match whitelist. Cek `ALLOWED_EMAIL_DOMAINS` di `.env`
- Loop redirect `/` → `/login` → cookie `multica_auth` tidak ter-set. Cek `FRONTEND_ORIGIN` env (untuk Secure flag), `COOKIE_DOMAIN`

---

### T5. Full Flow via Browser — Email di Luar Domain Whitelist (wajib)

**Tujuan:** Pastikan email di luar whitelist ditolak dengan redirect ke `/login?error=signup_prohibited`.

**Langkah:**
1. Akses `http://localhost:3000/auth/keycloak/login` di browser
2. Login di Keycloak dengan email yang **TIDAK ADA di `ALLOWED_EMAIL_DOMAINS`** (mis. `user@gmail.com`) dan belum terdaftar di Multica
3. Keycloak redirect balik ke callback

**Ekspektasi:**
- Browser redirect ke `/login?error=signup_prohibited`
- Halaman login menampilkan error (jika frontend Batch 4 sudah ada — jika belum, URL bar menunjukkan `?error=signup_prohibited`)
- Tidak ada cookie `multica_auth` ter-set

---

### T6. State Mismatch / CSRF (wajib)

**Tujuan:** Pastikan state cookie verification menolak callback dengan state yang tidak cocok.

```bash
# Kirim callback dengan state fake (tanpa state cookie yang valid)
curl -v -s -o /dev/null "http://localhost:3000/auth/keycloak/callback?code=fake&state=fake" 2>&1 | grep -E "HTTP/|Location"
```

**Ekspektasi:**
```
HTTP/1.1 302 Found
Location: /login?error=sso_failed
```

---

### T7. Cancel Login di Keycloak (opsional)

**Tujuan:** Pastikan batal login di Keycloak tidak crash aplikasi.

1. Akses `http://localhost:3000/auth/keycloak/login`
2. Di halaman Keycloak, tekan tombol Back di browser
3. **Ekspektasi:** kembali ke halaman login Multica tanpa crash/halaman putih. State cookie expired/mismatch → jika callback terpicu tanpa code, redirect ke `/login?error=sso_failed`.

---

### T8. User Existing di Luar Domain (opsional)

**Tujuan:** Pastikan user yang sudah terdaftar di DB Multica (dibuat via magic-link sebelumnya) tetap bisa login via SSO walau emailnya tidak di whitelist.

1. Pastikan ada user di tabel `user` dengan email di luar `ALLOWED_EMAIL_DOMAINS` (mis. `old-user@gmail.com`)
2. Login via Keycloak dengan email tersebut
3. **Ekspektasi:** login sukses — `findOrCreateUser` menemukan user existing → `checkSignupAllowed` mengembalikan nil (existing user selalu diizinkan) → cookie ter-set → landing di app

---

## Checklist Lolos / Gagal

| Skenario | Wajib | Status |
|----------|-------|--------|
| T1. Compile check | ✓ wajib | ☐ |
| T2. Login redirect → 302 ke Keycloak (SSO enabled) | ✓ wajib | ☐ |
| T3. Login redirect → 404 (SSO disabled) | ✓ wajib | ☐ |
| T4. Full flow browser — email di whitelist → sukses | ✓ wajib | ☐ |
| T5. Full flow browser — email di luar whitelist → ditolak | ✓ wajib | ☐ |
| T6. State mismatch → `/login?error=sso_failed` | ✓ wajib | ☐ |
| T7. Cancel login di Keycloak | opsional | ☐ |
| T8. User existing di luar domain | opsional | ☐ |

**Batch 3 dinyatakan LOLOS jika T1–T6 lulus.** T7–T8 opsional.

---

## Troubleshooting

| Gejala | Penyebab | Solusi |
|--------|----------|--------|
| `404` saat SSO enabled | Binary belum di-rebuild | `go build -o bin/server ./cmd/server` + restart |
| `302` ke `/login?error=sso_failed` | State cookie tidak ter-set/terkirim, atau PKCE gagal | Cek log `sso: state mismatch` / `sso: token exchange`; pastikan browser terima cookie `multica_sso_state` (SameSite=Lax, HttpOnly) |
| `302` ke `/login?error=signup_prohibited` | Email tidak match `ALLOWED_EMAIL_DOMAINS` / `ALLOWED_EMAILS` | Cek `.env` whitelist; cek apakah user sudah ada di DB (existing user selalu diizinkan) |
| Keycloak error: "Invalid redirect_uri" | `MULTICA_SSO_REDIRECT_URL` tidak terdaftar di Keycloak client | Tambahkan redirect URI ke Keycloak client `task-or` → Valid Redirect URIs |
| Keycloak error: "Invalid code_challenge" | PKCE mismatch | Pastikan `code_verifier` dari state cookie cocok dengan `code_challenge` yang dikirim. Jangan cache state cookie antar request |
| Loop redirect `/` → `/login` | Cookie `multica_auth` tidak ter-set atau tidak terkirim | Cek `FRONTEND_ORIGIN` (Secure flag), `COOKIE_DOMAIN`; pastikan browser terima cookie |
| `sso: token exchange` error di log | Keycloak unreachable / code expired / PKCE mismatch | Cek jaringan ke Keycloak; pastikan `MULTICA_SSO_SKIP_TLS_VERIFY=true` untuk internal CA |
| Callback 302 tapi tidak ke `/` | `next` param di state cookie | Cek `sanitizeNext` — hanya relative path `/` yang diizinkan |

---

## Setelah Lolos

Batch 3 selesai → lanjut ke **Batch 4: Frontend Config Flag + Login Button + i18n**. Batch 4 murni UI — backend sudah berfungsi penuh dari Batch 3. Tombol "Login with Keycloak" akan muncul di halaman login, dan error messages (`signup_prohibited`, `sso_failed`) akan ditampilkan.

Setelah Batch 4 di-push, di server jalankan:
```bash
cd /home/multica/multica
git pull origin dev_taskor
# Frontend perlu rebuild juga:
sudo systemctl restart multica-frontend
# Test: buka /login → tombol "Login with Keycloak" muncul
```