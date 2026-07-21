# Keycloak SSO Integration Testing Guide (Multica)

**Date:** 2026-07-17 (revised after alignment audit)

Panduan ini ditujukan untuk QA/Engineer yang akan memverifikasi fitur Keycloak SSO di platform Multica.

> **Perubahan dari draft sebelumnya:** tidak ada halaman `/unauthorized` (penolakan via `/login?error=signup_prohibited`), tidak ada Talita DB (mapping pakai whitelist `ALLOWED_EMAILS` + `findOrCreateUser` internal), dan route SSO ada di `/auth/keycloak/*` (tanpa prefix `/api`/`/v1`).

## Prasyarat Pengujian

Jalankan service Multica dengan environment variables SSO aktif **dan** whitelist email:

```bash
# Keycloak (Larasati)
MULTICA_SSO_ENABLED=true
MULTICA_SSO_KEYCLOAK_ISSUER=https://larasati.lintasarta.co.id/realms/dev
MULTICA_SSO_CLIENT_ID=task-or
MULTICA_SSO_CLIENT_SECRET=oV6mcQShpvBtogbTGgGkuzKZ09Fy1o3X
MULTICA_SSO_REDIRECT_URL=http://localhost:3000/auth/keycloak/callback
# Skip TLS verify untuk internal CA (Larasati)
MULTICA_SSO_SKIP_TLS_VERIFY=true

# Access control: domain whitelist — satu baris mencakup seluruh user di domain
ALLOW_SIGNUP=false
ALLOWED_EMAIL_DOMAINS=lintasarta.co.id
# (Opsional) ALLOWED_EMAILS=kontraktor@vendor.com — pengecualian per-individu di luar domain
```

Pastikan juga Keycloak client `task-or` di realm `dev` sudah dikonfigurasi:
- Access Type: `confidential`, Standard Flow `ON`, Direct Access Grants `OFF`
- Valid Redirect URIs: `http://localhost:3000/auth/keycloak/callback`
- Mapper `email` aktif di ID token

## Skenario Uji

### 1. Positif: Login SSO Berhasil (Email di Domain Ter-whitelist)
1. Buka halaman login Multica (`http://localhost:3000/login`).
2. Verifikasi terdapat tombol **"Login with Keycloak"** (muncul karena `sso_enabled=true` dari `/api/config`).
3. Klik tombol. Pastikan browser diarahkan (302) ke halaman login Keycloak Larasati (`https://larasati.lintasarta.co.id`).
4. Masukkan username + password Keycloak untuk user yang emailnya **domain-nya ADA di `ALLOWED_EMAIL_DOMAINS`** (mis. `user@lintasarta.co.id`), atau user yang sudah terdaftar di tabel `user` Multica.
5. Setelah login Keycloak, browser redirect balik ke `/auth/keycloak/callback`, lalu redirect ke `/` (atau URL `next`).
6. **Ekspektasi:** Pengguna otomatis masuk ke Dashboard/Workspace Multica tanpa kredensial tambahan. Cookie `multica_auth` (HttpOnly) ter-set; `AuthInitializer` memanggil `getMe()` dan user context terbentuk. Tidak ada halaman loading `/sso/callback` — langsung ke app.

### 2. Negatif: Login SSO Ditolak (Email di Luar Domain Ter-whitelist)
1. Buka halaman login Multica dan klik **"Login with Keycloak"**.
2. Masukkan kredensial Keycloak untuk user yang emailnya **domain-nya TIDAK ADA di `ALLOWED_EMAIL_DOMAINS`** (mis. `user@gmail.com`) dan belum terdaftar di Multica.
3. Setelah login Keycloak berhasil, browser redirect balik ke Multica.
4. **Ekspektasi:** Backend memanggil `findOrCreateUser` → `checkSignupAllowed` → `ErrSignupProhibited` → redirect ke `/login?error=signup_prohibited`.
5. Halaman login menampilkan pesan error: *"Your account is not authorized. Please contact Admin IT."* (atau terjemahan sesuai locale).
6. **Tidak ada** halaman `/unauthorized` terpisah — penolakan ditangani di halaman login yang sama.

### 3. Negatif: Batal Login di Keycloak (Cancel / Back)
1. Klik **"Login with Keycloak"** dari halaman login Multica.
2. Di halaman Keycloak, jangan login — tekan tombol Back di browser.
3. **Ekspektasi:** Aplikasi Multica tetap menampilkan halaman login tanpa crash/halaman putih. State cookie `multica_sso_state` sudah expired/mismatch; bila callback terpicu tanpa code, redirect ke `/login?error=sso_failed`.

### 4. Negatif: State Mismatch / CSRF
1. Mulai flow SSO, lalu di URL callback ubah parameter `state` secara manual sebelum submit.
2. **Ekspektasi:** Backend mendeteksi `state` tidak cocok dengan state cookie → redirect ke `/login?error=sso_failed`. Tidak ada token yang di-issue.

### 5. Positif: Fallback Login (SSO Dimatikan)
1. Hentikan service Multica.
2. Ubah `MULTICA_SSO_ENABLED=false` (atau hapus variable).
3. Nyalakan kembali service.
4. Buka halaman login Multica.
5. **Ekspektasi:** Tombol "Login with Keycloak" **tidak muncul** (`/api/config` tidak mengembalikan `sso_enabled`).
6. **Ekspektasi Lanjutan:** Pengguna masih bisa masuk dengan magic-link (email + kode) dan Google OAuth (jika `GOOGLE_CLIENT_ID` ter-set). Fitur eksisting tidak rusak.
7. Akses langsung `GET /auth/keycloak/login` → **404** (handler no-op saat SSO disabled).

### 6. Positif: User Sudah Terdaftar Lewat Jalur Lain
1. User sudah punya akun Multica (dibuat via magic-link sebelumnya) dengan email yang **domain-nya tidak ada di `ALLOWED_EMAIL_DOMAINS`** tapi sudah ada di tabel `user`.
2. Login via Keycloak dengan email tersebut.
3. **Ekspektasi:** `findOrCreateUser` menemukan user existing → `checkSignupAllowed` mengembalikan nil (existing user selalu diizinkan) → login berhasil. Ini konsisten dengan behavior `VerifyCode`/`GoogleLogin` yang sudah ada.

### 6b. Positif: Pengecualian Per-Individu via ALLOWED_EMAILS
1. Kontraktor dengan email `contractor@vendor.com` (domain `vendor.com` **tidak** ada di `ALLOWED_EMAIL_DOMAINS`) ditambahkan ke `ALLOWED_EMAILS=contractor@vendor.com`.
2. Restart backend (env tidak hot-reload).
3. Login via Keycloak dengan email tersebut.
4. **Ekspektasi:** `checkSignupAllowed` match di whitelist eksplisit `ALLOWED_EMAILS` (cek pertama, menang sebelum domain) → login berhasil. Berguna untuk memberi akses individu di luar domain tanpa membuka seluruh domain.

### 7. Verifikasi Cookie & SameSite
1. Setelah login SSO sukses, buka DevTools → Application → Cookies.
2. **Ekspektasi:** `multica_auth` (HttpOnly, SameSite=Strict, Secure jika HTTPS) dan `multica_csrf` (SameSite=Strict) ter-set. `multica_sso_state` sudah ter-clear (single-use).
3. Refresh halaman — `getMe()` dipanggil dengan cookie `multica_auth` terkirim otomatis → user tetap login.

---

### 8. Single Logout (SLO) — Session Keycloak Harus Hapus

**Tujuan:** Pastikan logout memicu redirect ke Keycloak `end_session_endpoint`, menghapus session Keycloak (bukan hanya cookie Multica).

1. Login via SSO (skenario 1).
2. Setelah masuk app, klik tombol **Logout** di sidebar/settings.
3. **Ekspektasi:**
   - Browser redirect ke `/auth/keycloak/logout` (backend).
   - Backend clear cookie Multica + redirect ke `https://larasati.lintasarta.co.id/realms/dev/protocol/openid-connect/logout?post_logout_redirect_uri=...&client_id=task-or`.
   - Keycloak destroy session + redirect balik ke `/login`.
   - Browser landing di `/login`.
4. **Verifikasi SLO berhasil:** klik "Login with Keycloak" lagi → **harus diminta password lagi** (tidak langsung masuk). Jika langsung masuk tanpa password, SLO gagal — session Keycloak tidak terhapus.

> **Prasyarat Keycloak client:** `task-or` client harus punya `https://task-or.lintasarta.co.id/login` di **Valid Post Logout Redirect URIs**. Tanpa ini, Keycloak menolak post-logout redirect dan user landing di error page Keycloak.

### 9. Logout saat SSO Disabled — Fallback Magic-Link

**Tujuan:** Pastikan logout tetap jalan (client-side push ke `/login`) saat SSO dimatikan.

1. Set `MULTICA_SSO_ENABLED=false`, restart backend + rebuild frontend.
2. Login via magic-link (email + kode).
3. Klik Logout.
4. **Ekspektasi:** redirect ke `/login` (client-side navigation, bukan full-page redirect ke `/auth/keycloak/logout`).

---

## Deployment Notes (server Ubuntu)

> **Penting untuk Batch 4:** Batch ini mengubah **backend + frontend**. Setelah `git pull` di server:
>
> - **Backend:** rebuild binary (`cd server && go build -o bin/server ./cmd/server`) + restart `multica-backend` (`sudo systemctl restart multica-backend`)
> - **Frontend:** **wajib rebuild** (`cd apps/web && pnpm build`) + restart `multica-frontend` (`sudo systemctl restart multica-frontend`). Restart systemd saja **tidak cukup** — Next.js production server menjalankan build hasil `pnpm build` yang sudah ter-compile. Tanpa rebuild, perubahan `login-page.tsx` / `auth-initializer.tsx` / config store tidak akan terlihat di browser.
> - Verifikasi: `ls -la apps/web/.next` — timestamp harus lebih baru dari `git pull`. Cek `curl /api/config | grep sso_enabled` → `"sso_enabled": true`.

---

## Troubleshooting Guide (Admin/Developer)

Jika skenario 1 atau 2 gagal, cek log backend Multica:

- **Error koneksi OIDC / discovery:** Pastikan `https://larasati.lintasarta.co.id/realms/dev/.well-known/openid-configuration` dapat diakses dari server Multica dan mengembalikan JSON. Jika gagal saat startup, backend `os.Exit(1)` dengan log `sso: keycloak oidc init failed`.
- **`sso_failed` terus-menerus:** Cek log `sso.token_exchange` / state mismatch. Pastikan `MULTICA_SSO_REDIRECT_URL` persis sama dengan Valid Redirect URI di Keycloak client, dan cookie `multica_sso_state` tidak diblokir browser (SameSite=Lax, HttpOnly).
- **`signup_prohibited` padahal email seharusnya boleh:** Cek `ALLOWED_EMAIL_DOMAINS` — pastikan domain email user persis terdaftar (case-insensitive). Untuk user di luar domain, cek `ALLOWED_EMAILS` (koma-separated, case-insensitive). Pastikan `ALLOW_SIGNUP=false` tidak memblokir user existing — user existing selalu diizinkan. Ingat: env tidak hot-reload, perlu restart backend.
- **Tombol SSO tidak muncul:** Pastikan `MULTICA_SSO_ENABLED=true` dan `GET /api/config` mengembalikan `"sso_enabled": true`. Cek `useConfigStore.ssoEnabled` di frontend.
- **Cookie tidak ter-set di localhost HTTP:** `Secure` flag mengikuti scheme `FRONTEND_ORIGIN`. Untuk dev HTTP, pastikan `FRONTEND_ORIGIN=http://localhost:3000` agar `Secure=false`.
- **Loop redirect `/` → `/login`:** Cookie `multica_auth` tidak terkirim setelah redirect callback → `/`. Pastikan tidak ada proxy yang strip cookie, dan `COOKIE_DOMAIN` kosong untuk single-host.