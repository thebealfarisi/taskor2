# Testing Guide — Batch 4: Frontend Config Flag + Login Button + SLO + i18n

**Date:** 2026-07-17
**Reference:** `support_docs/SSO/multica/development_plan_keycloak_sso_phased.md` (Batch 4)
**Scope:** Verifikasi bahwa frontend menampilkan tombol "Login with Keycloak" saat SSO aktif, error messages dari callback tampil, dan logout memicu Single Logout (SLO) ke Keycloak.

---

## Environment

| Aktivitas | Lokasi | Shell |
|-----------|--------|-------|
| **Development** (edit kode, commit, push) | Windows: `D:\Kerjaan\Project\taskor2` | PowerShell |
| **Testing** (build, run, verify) | Ubuntu server: `/home/multica/multica` | Bash |

### Deployment Topology (server Ubuntu)

- **Backend:** systemd `multica-backend` (`/home/multica/multica/server/bin/server`)
- **Frontend:** systemd `multica-frontend`
- **Project root:** `/home/multica/multica`

---

## Perubahan yang Diuji

| File | Perubahan |
|------|-----------|
| `packages/core/config/index.ts` | MODIFY — `ssoEnabled` field di `ConfigState` + `setAuthConfig` |
| `packages/core/api/schemas.ts` | MODIFY — `sso_enabled?` field di `AppConfigResponse` |
| `packages/core/platform/auth-initializer.tsx` | MODIFY — pass `sso_enabled` ke `setAuthConfig` |
| `packages/views/auth/login-page.tsx` | MODIFY — `ssoEnabled` prop, "Login with Keycloak" button, `?error=` messages, `sanitizeSsoNext` |
| `packages/views/auth/use-logout.ts` | MODIFY — SLO: redirect ke `/auth/keycloak/logout` saat `ssoEnabled` |
| `packages/views/locales/{en,zh-Hans,ko,ja}/auth.json` | MODIFY — `sso.button` / `sso.unauthorized` / `sso.failed` |
| `apps/web/app/(auth)/login/page.tsx` | MODIFY — pass `ssoEnabled` dari `useConfigStore` ke `<LoginPage>` |

---

## Persiapan: Pull + Rebuild

> **Penting:** Batch 4 mengubah file frontend (`packages/views/auth/login-page.tsx`, `packages/core/config/index.ts`, `packages/core/platform/auth-initializer.tsx`, `apps/web/app/(auth)/login/page.tsx`, 4 locale `auth.json`). Restart systemd `multica-frontend` **tidak cukup** — Next.js production server menjalankan build hasil `pnpm build` yang sudah ter-compile. Anda **wajib rebuild** frontend setelah `git pull`, baru restart service.

```bash
cd /home/multica/multica
git pull origin dev_taskor

# --- Backend (ada perubahan SLO handler di Batch 4) ---
cd server
go build -o bin/server ./cmd/server
sudo systemctl restart multica-backend

# --- Frontend (WAJIB rebuild — restart saja tidak cukup) ---
cd /home/multica/multica
pnpm install --frozen-lockfile   # jika lockfile berubah
cd apps/web
pnpm build                        # compile ulang Next.js production bundle
sudo systemctl restart multica-frontend
sleep 3
sudo systemctl status multica-frontend --no-pager | head -5
```

### Verifikasi build berhasil

```bash
# Cek timestamp .next — harus lebih baru dari git pull
ls -la /home/multica/multica/apps/web/.next | head -3

# Cek config response punya sso_enabled
curl -s https://task-or.lintasarta.co.id/api/config | python3 -m json.tool | grep -i sso
# Ekspektasi: "sso_enabled": true
```

Jika `pnpm build` gagal (mis. OOM, lockfile mismatch), lihat troubleshooting di bawah.

### Pastikan `.env` punya SSO config:

```bash
grep MULTICA_SSO /home/multica/multica/.env
# Ekspektasi: MULTICA_SSO_ENABLED=true + issuer + client + secret + redirect + skip_tls
```

---

## Skenario Uji

### T1. Typecheck (wajib)

```bash
cd /home/multica/multica
pnpm typecheck
```

**Ekspektasi:** tidak ada error TypeScript.

---

### T2. Tombol "Login with Keycloak" Muncul — SSO Enabled (wajib)

1. Buka `https://task-or.lintasarta.co.id/login` di browser
2. **Ekspektasi:** tombol **"Login with Keycloak"** muncul di halaman login, di bawah tombol "Continue" (dengan divider "or" di antaranya)
3. Verifikasi via DevTools:
   - Buka DevTools → Network → reload `/login`
   - Cek response `GET /api/config` → ada field `"sso_enabled": true`
   - Buka Console → ketik: `useConfigStore` (jika exposed) atau inspect React component

---

### T3. Tombol Hilang — SSO Disabled (wajib)

```bash
sed -i 's/MULTICA_SSO_ENABLED=true/MULTICA_SSO_ENABLED=false/' /home/multica/multica/.env
sudo systemctl restart multica-backend
sleep 2
```

1. Refresh `https://task-or.lintasarta.co.id/login`
2. **Ekspektasi:** tombol "Login with Keycloak" **tidak muncul**
3. Verifikasi: `GET /api/config` → tidak ada field `sso_enabled` (omitempty)

**Kembalikan ke enabled:**
```bash
sed -i 's/MULTICA_SSO_ENABLED=false/MULTICA_SSO_ENABLED=true/' /home/multica/multica/.env
sudo systemctl restart multica-backend
sleep 2
```

---

### T4. Klik Tombol → Redirect ke Keycloak (wajib)

1. Buka `https://task-or.lintasarta.co.id/login`
2. Klik **"Login with Keycloak"**
3. **Ekspektasi:** browser redirect ke `https://larasati.lintasarta.co.id/realms/dev/protocol/openid-connect/auth?...`
4. URL mengandung `client_id=task-or`, `code_challenge`, `state`, `redirect_uri`

---

### T5. Full Flow — Email di Domain Whitelist (wajib)

1. Klik "Login with Keycloak"
2. Login di Keycloak dengan email di `ALLOWED_EMAIL_DOMAINS` (mis. `user@lintasarta.co.id`)
3. **Ekspektasi:** redirect balik → cookie `multica_auth` ter-set → landing di app
4. User ter-authenticate, bisa akses dashboard

---

### T6. Error Message — signup_prohibited (wajib)

1. Klik "Login with Keycloak"
2. Login di Keycloak dengan email di luar `ALLOWED_EMAIL_DOMAINS` (mis. `user@gmail.com`), user belum terdaftar
3. **Ekspektasi:** redirect ke `/login?error=signup_prohibited`
4. Halaman login menampilkan pesan: **"Your account is not authorized. Please contact Admin IT."** (atau terjemahan sesuai locale browser)

---

### T7. Error Message — sso_failed (wajib)

1. Buka langsung `https://task-or.lintasarta.co.id/login?error=sso_failed` di browser
2. **Ekspektasi:** halaman login menampilkan pesan: **"SSO failed, please try again."**

---

### T8. Single Logout (SLO) (wajib)

**Tujuan:** Pastikan logout memicu redirect ke Keycloak end_session_endpoint, menghapus session Keycloak.

1. Login via SSO (T5)
2. Setelah masuk app, klik tombol **Logout** di sidebar/settings
3. **Ekspektasi:**
   - Browser redirect ke `/auth/keycloak/logout` (backend)
   - Backend clear cookie Multica + redirect ke `https://larasati.lintasarta.co.id/realms/dev/protocol/openid-connect/logout?post_logout_redirect_uri=...&client_id=task-or`
   - Keycloak destroy session + redirect balik ke `/login`
   - Browser landing di `/login`
4. **Verifikasi SLO berhasil:** klik "Login with Keycloak" lagi → **harus diminta password lagi** (tidak langsung masuk). Jika langsung masuk, SLO gagal — session Keycloak tidak terhapus.

---

### T9. Logout saat SSO Disabled (opsional)

**Tujuan:** Pastikan logout tetap jalan (client-side push ke /login) saat SSO dimatikan.

```bash
sed -i 's/MULTICA_SSO_ENABLED=true/MULTICA_SSO_ENABLED=false/' /home/multica/multica/.env
sudo systemctl restart multica-backend
sleep 2
```

1. Login via magic-link (email + kode)
2. Klik Logout
3. **Ekspektasi:** redirect ke `/login` (client-side navigation, bukan full-page redirect ke `/auth/keycloak/logout`)

**Kembalikan:**
```bash
sed -i 's/MULTICA_SSO_ENABLED=false/MULTICA_SSO_ENABLED=true/' /home/multica/multica/.env
sudo systemctl restart multica-backend
```

---

### T10. i18n — Locale Switch (opsional)

**Tujuan:** Pastikan tombol & error messages ter-translate di locale lain.

1. Buka `https://task-or.lintasarta.co.id/login?error=signup_prohibited` dengan browser language set ke:
   - **zh-Hans** → "您的账号未获授权，请联系 IT 管理员。"
   - **ko** → "계정이 승인되지 않았습니다. IT 관리자에게 문의하세요."
   - **ja** → "アカウントが承認されていません。IT管理者にお問い合わせください。"
2. **Ekspektasi:** pesan error tampil dalam bahasa yang sesuai

---

## Checklist Lolos / Gagal

| Skenario | Wajib | Status |
|----------|-------|--------|
| T1. Typecheck | ✓ wajib | ✅ LOLOS (commit `9166986f`) |
| T2. Tombol muncul saat SSO enabled | ✓ wajib | ☐ |
| T3. Tombol hilang saat SSO disabled | ✓ wajib | ☐ |
| T4. Klik tombol → redirect ke Keycloak | ✓ wajib | ☐ |
| T5. Full flow — email di whitelist → sukses | ✓ wajib | ☐ |
| T6. Error message — signup_prohibited | ✓ wajib | ☐ |
| T7. Error message — sso_failed | ✓ wajib | ☐ |
| T8. Single Logout (SLO) — diminta password lagi | ✓ wajib | ☐ |
| T9. Logout saat SSO disabled | opsional | ☐ |
| T10. i18n locale switch | opsional | ☐ |

**Batch 4 dinyatakan LOLOS jika T1–T8 lulus.** T9–T10 opsional.

---

## Troubleshooting

| Gejala | Penyebab | Solusi |
|--------|----------|--------|
| Tombol SSO tidak muncul padahal `MULTICA_SSO_ENABLED=true` | Frontend belum rebuild (`.next` masih build lama), atau `sso_enabled` tidak sampai ke response | **Rebuild frontend**: `cd apps/web && pnpm build && sudo systemctl restart multica-frontend`; cek `ls -la .next` timestamp baru; cek `curl /api/config \| grep sso_enabled`; cek `auth-initializer.tsx` pass `ssoEnabled` |
| `pnpm build` OOM / gagal di server | Memory tidak cukup untuk Next.js production build | Tambah swap: `sudo fallocate -l 2G /swapfile && sudo mkswap /swapfile && sudo swapon /swapfile`; atau build dengan `NODE_OPTIONS=--max-old-space-size=2048 pnpm build` |
| Browser masih tampilkan halaman lama setelah rebuild | Browser cache / CDN cache | Hard refresh (Ctrl+Shift+R); cek `?v=` cache buster; disable browser cache di DevTools Network tab |
| Tombol muncul tapi klik tidak redirect | `handleKeycloakLogin` tidak ter-trigger | Cek Console untuk JS error; pastikan `window.location.href` di-set |
| Error message tidak tampil | `useSsoError` hook tidak baca `?error=` | Cek URL bar ada `?error=signup_prohibited`; cek i18n key `sso.unauthorized` ada di auth.json |
| Logout tidak redirect ke Keycloak | `configStore.ssoEnabled` false saat logout | Cek `use-logout.ts` baca `configStore.getState().ssoEnabled`; pastikan config sudah ter-fetch |
| Setelah SLO, klik login langsung masuk (tanpa password) | Keycloak session tidak ter-hapus | Cek Keycloak client `task-or` → "Valid Post Logout Redirect URIs" ada `https://task-or.lintasarta.co.id/login`; cek `FRONTEND_ORIGIN` env di backend |
| Keycloak tampil "Are you sure?" saat logout | `client_id` tidak dikirim ke end_session_endpoint | Cek backend log `sso: build logout url`; pastikan `LogoutURL` set `client_id` |
| Typecheck error: `ssoEnabled` not found | Config store / login-page tidak sync | Pastikan `packages/core/config/index.ts` + `login-page.tsx` + `apps/web/.../login/page.tsx` semua ter-update |
| Typecheck error: `Property 'sso' does not exist on type` di `login-page.tsx` | `useSsoError` helper pakai `t: ReturnType<typeof useT>[0]` (union semua namespace), `$.sso` hanya ada di namespace `auth` | Fix commit `9166986f`: inline efek ke component body dimana `t` sudah scoped via `useT("auth")`. Pastikan pull commit terbaru. |

---

## Setelah Lolos

Batch 4 selesai → lanjut ke **Batch 5: E2E Testing & Verification**. Batch 5 tidak menulis kode — hanya testing menyeluruh mengikuti `testing_guide_keycloak_sso.md` (9 skenario test matrix).