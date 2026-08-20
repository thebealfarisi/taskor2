# Analisis Merge: `dev_taskor` → `main`

Tanggal analisis awal: 2026-08-18  
**Tanggal eksekusi merge selesai:** 2026-08-20  
Tujuan: Inventarisasi file bentrok dan rekam jejak resolusi merge `dev_taskor` dengan `main` yang sudah di-sync dari repo upstream.

> [!NOTE]
> **STATUS SAAT INI (2026-08-20): MERGE SELESAI & SUKSES**
> Branch `main` lokal telah di-pull dari `origin/main` (`8c9b7503a`), `dev_taskor` di-merge dengan `main`, seluruh 3 konflik tersisa telah diselesaikan, verifikasi `pnpm typecheck` dan `pnpm test` + `go test` lulus 100%, dan branch `main` lokal telah di-fast-forward ke commit merge akhir (`87b738f04`).

## Status Branch Pasca Merge

| Item | Nilai |
|---|---|
| Branch sumber | `dev_taskor` (`87b738f04`) |
| Branch target | `main` (`87b738f04`, synchronized with `dev_taskor`) |
| Remote upstream | `origin/main` (`8c9b7503a`) |
| Result commit | `87b738f043f79c9f0b10b4c63be48447e3287982` |
| Verifikasi | `pnpm typecheck` (9 packages OK), `pnpm test` (7 files OK), `go test ./...` (ALL OK) |
| Backup | `dev_taskor_bak` (`2c7d1754b`) |

## Ringkasan Hasil

| Kategori | Jumlah |
|---|---|
| **Konflik aktual** (simulasi merge) | **10 file / 14 zona konflik** |
| Berubah di kedua sisi, tapi auto-merge bersih | 15 file |
| Hanya diubah `dev_taskor` (aman) | 32 file |
| Hanya diubah `main`/upstream (aman) | 3.236 file |

Metode: `git merge-tree --write-tree` (deteksi file yang konflik) + `git merge-file --diff3` per file (deteksi zona konflik persis, per hunk). Keduanya simulasi tanpa menyentuh working tree.

## Insight Utama

**Seluruh 14 zona konflik bersifat ADDITIVE** — kedua sisi menambahkan hal *berbeda* di titik sisipan yang *sama* (atau mengubah aspek berbeda pada baris yang sama). Tidak ada konflik yang saling eksklusif: tidak ada perubahan `dev_taskor` yang harus dibuang demi `main`, maupun sebaliknya. Pola resolusi hampir selalu: **ambil struktur upstream terbaru, gabungkan tambahan milik dev**.

| File | Zona | Pola bentrok | Resolusi singkat |
|---|---|---|---|
| `.env.example` | 1 | Dua blok baru di anchor sama (SSO vs WeCom) | Gabung kedua blok |
| `apps/web/app/auth/callback/page.tsx` | 1 | Teks (dev) vs className (main) di baris sama | Gabung: className main + teks dev |
| `apps/web/app/layout.tsx` | 2 | String literal vs konstanta module; cache-bust vs apple icon | Adopsi pola main, isi branding dev; gabung icon |
| `packages/core/config/index.ts` | 2 | Field baru berbeda di signature/body fungsi sama | Gabung kedua field |
| `packages/core/platform/auth-initializer.tsx` | 1 | Field baru berbeda di object literal sama | Gabung kedua field |
| `packages/views/auth/use-logout.ts` | 1 | Import baru berbeda di posisi sama | Gabung kedua import |
| `server/cmd/server/router.go` | 2 | Field Config & blok setup berbeda di titik sama | Gabung keduanya |
| `server/internal/handler/handler.go` | 1 | Field Config struct berbeda di ujung sama | Gabung keduanya |
| `server/go.mod` | 2 | Dep baru (dev) vs bump versi (main) | Gabung, ambil versi main, `go mod tidy` |
| `server/go.sum` | 1 | Checksum versi lama vs baru | Ambil sisi main + `go mod tidy` (jangan hand-merge) |

---

## A. Detail Perbandingan per File Konflik (10 file / 14 zona)

Konvensi: **dev** = `dev_taskor`, **main** = `origin/main` (upstream terbaru), **base** = merge base `2d13b26f`. Nomor baris merujuk ke hasil simulasi merge — gunakan sebagai penanda lokasi relatif, bukan absolut.

### A1. `.env.example` — 1 zona

- **dev menambah:** blok konfigurasi Keycloak SSO setelah `GOOGLE_REDIRECT_URI` — `MULTICA_SSO_ENABLED`, `MULTICA_SSO_KEYCLOAK_ISSUER`, `MULTICA_SSO_CLIENT_ID`, `MULTICA_SSO_CLIENT_SECRET`, `MULTICA_SSO_REDIRECT_URL`, `MULTICA_SSO_SKIP_TLS_VERIFY` (+ komentar access control).
- **main menambah:** blok WeCom smart-bot di anchor yang sama (`MULTICA_WECOM_SECRET_KEY`, `MULTICA_WECOM_MEDIA_ALLOW_CIDRS`, `MULTICA_WECOM_TRACE`). Puluhan perubahan lain di file ini (PORT, default LLM model, COOKIE_DOMAIN, rate-limit invitation, VCS, DingTalk, PostHog, dll) auto-merge bersih.
- **Zona konflik (~baris 185-280):** kedua sisi menyisipkan blok baru di titik kosong yang sama (setelah `GOOGLE_REDIRECT_URI`, sebelum blok S3).
- **Resolusi:** gabungkan kedua blok; urutan bebas, keduanya independen.

### A2. `apps/web/app/auth/callback/page.tsx` — 1 zona

- **dev mengubah:** rebrand teks UI Multica → Super-Presales di 3 tempat (`CardTitle`, `CardDescription`, `Button`). Perubahan di `CardDescription` dan `Button` auto-merge bersih.
- **main mengubah:** migrasi typography token — `className` 3 `CardTitle` dari `text-2xl` → `text-display-sm`.
- **Zona konflik (~baris 151-157):** `CardTitle` state `desktopToken` — dev mengubah **teks**, main mengubah **className**, di baris yang sama.
- **Resolusi:** gabungkan keduanya menjadi:
  ```tsx
  <CardTitle className="text-display-sm">Opening Super-Presales</CardTitle>
  ```

### A3. `apps/web/app/layout.tsx` — 2 zona

- **dev mengubah:** metadata rebrand — `title.default` dan `title.template` Super-Presales (string hardcoded), `siteName`; favicon cache-bust `/favicon.svg?v=2` pada `icon` dan `shortcut`. (`siteName` auto-merge bersih.)
- **main mengubah (refactor):** title memakai konstanta `SITE_TITLE`/`TITLE_TEMPLATE` dari `@/platform/document-title`; import `resolveBrowserApiBaseUrl`/`resolveBrowserWsUrl` dari `@/config/runtime-urls`; font Inter ditambah `style: ["normal","italic"]`; tambah `apple` touch icon + blok `appleWebApp`; pass `apiBaseUrl`/`wsUrl` ke `WebProviders`. (Selain 2 zona konflik, semua auto-merge bersih.)
- **Zona 1 (~baris 78-87) — `metadata.title`:** string literal (dev) vs konstanta module (main), baris yang sama.
- **Zona 2 (~baris 92-117) — `metadata.icons`:** cache-bust `?v=2` (dev) vs `apple` icon + blok `appleWebApp` (main), blok yang sama.
- **Resolusi zona 1:** adopsi pola main (`SITE_TITLE`/`TITLE_TEMPLATE`), lalu update nilai konstanta di `apps/web/platform/document-title.ts` ke branding Super-Presales — lebih maintainable daripada string hardcode.
- **Resolusi zona 2:** gabungkan — pertahankan `?v=2` pada `icon`/`shortcut` DAN tambahkan `apple` icon + blok `appleWebApp` dari main (sebaiknya `appleWebApp.title` ikut di-rebrand ke Super-Presales).

### A4. `packages/core/config/index.ts` — 2 zona

- **dev menambah:** field `ssoEnabled: boolean` di interface `ConfigState`; `ssoEnabled?: boolean` di param `setAuthConfig`; default `ssoEnabled: false`; set di implementasi `setAuthConfig`.
- **main menambah:** field `vcsIntegrationAvailable: boolean` + `serverVersion: string` di interface; `vcsIntegrationAvailable?: boolean` di param; method baru `setServerVersion`; default untuk keduanya. (Field interface, default, dan method auto-merge bersih.)
- **Zona 1 (~baris 38-43) — tipe param `setAuthConfig`:** `ssoEnabled?` (dev) vs `vcsIntegrationAvailable?` (main) di posisi yang sama.
- **Zona 2 (~baris 66-79) — implementasi `setAuthConfig`:** keduanya memodifikasi signature + body fungsi yang sama.
- **Resolusi:** gabungkan kedua field di kedua zona. Implementasi zona 2 menjadi:
  ```ts
  setAuthConfig: ({
    allowSignup,
    googleClientId = "",
    workspaceCreationDisabled = false,
    ssoEnabled = false,
    vcsIntegrationAvailable = false,
  }) =>
    set({ allowSignup, googleClientId, workspaceCreationDisabled, ssoEnabled, vcsIntegrationAvailable }),
  ```

### A5. `packages/core/platform/auth-initializer.tsx` — 1 zona

- **dev menambah:** `ssoEnabled: cfg.sso_enabled === true` ke pemanggilan `configStore.getState().setAuthConfig(...)` di dalam `loadConfig`.
- **main mengubah (refactor besar, ~250 baris):** `loadConfig` diekstrak ke `useCallback` dengan retry/recovery (`RECOVERY_RETRY_DELAYS_MS`, listener event `online`), status auth `recovering`, handling `ApiError`, `workspaceListOptions` menggantikan `workspaceKeys`, `setServerVersion`. **Seluruh refactor ini auto-merge bersih** — hanya 1 titik yang bentrok.
- **Zona konflik (~baris 75-82):** object literal `setAuthConfig` — dev menambah `ssoEnabled`, main menambah `vcsIntegrationAvailable` di posisi yang sama (setelah `workspaceCreationDisabled`).
- **Resolusi:** tambahkan kedua field:
  ```ts
  workspaceCreationDisabled: cfg.workspace_creation_disabled === true,
  // SSO flag for the login page's Keycloak button.
  ssoEnabled: cfg.sso_enabled === true,
  // Absent/false on managed cloud and older servers.
  vcsIntegrationAvailable: cfg.vcs_integration_available === true,
  ```

### A6. `packages/views/auth/use-logout.ts` — 1 zona

- **dev menambah:** import `configStore` dari `@multica/core/config`; logic SSO Single Logout di callback — jika `ssoEnabled` aktif, redirect `window.location.href = "/auth/keycloak/logout"` lalu return (sebelum `push(paths.login())`). Logic body ini auto-merge bersih.
- **main menambah:** import `resetAllRegisteredDrafts` dari `@multica/core/drafts/cleanup-registry` + pemanggilannya di awal callback. Auto-merge bersih.
- **Zona konflik (~baris 8-13):** blok import — kedua sisi menambah import berbeda di posisi yang sama.
- **Resolusi:** pertahankan kedua import:
  ```ts
  import { clearWorkspaceStorage, defaultStorage } from "@multica/core/platform";
  import { configStore } from "@multica/core/config";
  import { resetAllRegisteredDrafts } from "@multica/core/drafts/cleanup-registry";
  import { paths } from "@multica/core/paths";
  ```

### A7. `server/cmd/server/router.go` — 2 zona

- **dev menambah:** import `server/internal/sso`; 6 field SSO di literal `signupConfig` (`SSOEnabled`, `SSOIssuer`, `SSOClientID`, `SSOClientSecret`, `SSORedirectURL`, `SSOSkipTLSVerify`, dibaca dari env `MULTICA_SSO_*`); blok setup OIDC setelah `handler.New(...)` — env-gated `if signupConfig.SSOEnabled`, OIDC discovery saat startup, `os.Exit(1)` bila gagal, assign `h.OIDC`; 3 route publik Keycloak (`GET /auth/keycloak/login`, `/auth/keycloak/callback`, `/auth/keycloak/logout`). **Route Keycloak auto-merge bersih** (disisipkan di antara `/auth/logout` dan `// Public API`, area yang tidak disentuh main).
- **main menambah (sangat luas):** import `fmt`, `dingtalk`, `wecom`; var CORS (`corsAllowedHeaders`/`corsExposedHeaders`); func baru (`normalizeServerVersion`, `buildChannelSupervisor`, `channelSupervisorConfigFromEnv`, `strictPositiveDurationEnv`); field `RouterOptions` baru (`ChannelLeaseMetrics`, `ChannelLeaseRedis`, `WecomMetrics`); `VCSIntegrationEnabled` + `ServerVersion` di Config; setup invitation rate limiters setelah `handler.New`; blok integrasi besar (DingTalk, WeCom, VCS encryption, Remote MCP Plugin, channel media reconciler, Redis stores); puluhan route baru (attachments, avatars, share-links, VCS webhooks, plugin OAuth, daemon tasks, issue table/properties, quick-actions, agent-builder, dll).
- **Zona 1 (~baris 345-355) — literal `signupConfig`:** dev menambah 6 field SSO; main menambah `ServerVersion: normalizeServerVersion(version)` — di ujung yang sama.
- **Zona 2 (~baris 358-383) — setelah `h := handler.New(...)`:** blok init SSO OIDC (dev) vs blok invitation rate limiters (main) — titik sisip yang sama, keduanya independen.
- **Resolusi:** gabungkan per zona. Zona 1: 6 field SSO + `ServerVersion`. Zona 2: saran urutan — invitation rate limiters dulu (mengikuti struktur upstream), lalu blok SSO OIDC setelahnya.

### A8. `server/internal/handler/handler.go` — 1 zona

- **dev menambah:** import `server/internal/sso`; 6 field SSO di `Config` struct; field `OIDC *sso.OIDCClient` di `Handler` struct (auto-merge bersih — main menambah field-field-nya di posisi lain).
- **main menambah:** import baru (`math`, `strings`, `dingtalk`, `ghsnapshot`, `wecom`, `secretbox`); `VCSIntegrationEnabled` + `ServerVersion` di Config; interface `DaemonPendingWorkNotifier`; belasan field `Handler` baru (`PluginService`, `DaemonPendingWork`, `ModelCatalogCache`, `InvitationRateLimiters`, `WebhookDeliveryWorker`, `ChannelMediaReconciler`, `DingTalkInstall`, `WecomStore`, `VCSSecretBox`, `PRRefresh`, dll); refactor `New()`; func baru (`writeErrorCode`, `DeclareChannelFileDelivery`, `issuePrefixForWorkspace`, dll); perbaikan `resolveIssueByIdentifier` dan `splitIdentifier`.
- **Zona konflik (~baris 128-150) — `Config` struct:** blok 6 field SSO (dev) vs `ServerVersion string` (main) — di ujung yang sama, pola persis seperti router.go zona 1.
- **Resolusi:** gabungkan — blok field SSO + field `ServerVersion` (urutan bebas).

### A9. `server/go.mod` — 2 zona

- **dev menambah:** direct deps `github.com/coreos/go-oidc/v3 v3.20.0` dan `golang.org/x/oauth2 v0.36.0` (+ `go-jose/v4` sebagai indirect, auto-merge bersih).
- **main mengubah:** `go 1.26.1` → `go 1.26.6` (bersih); bump `chi/v5 v5.2.5` → `v5.3.0`; tambah `yuin/goldmark v1.8.4`; bump `x/sync v0.20.0` → `v0.22.0`, `x/sys v0.35.0` → `v0.47.0`, `x/net` dan `x/text` ikut naik; tambah indirect `x/mod`, `x/telemetry`, `x/tools`, `x/vuln`; tambah directive `tool golang.org/x/vuln/cmd/govulncheck` (bersih).
- **Zona 1 (~baris 11-18):** dev menyisipkan `go-oidc/v3 v3.20.0` tepat sebelum `chi/v5 v5.2.5`; main mengubah baris chi menjadi `v5.3.0`.
- **Zona 2 (~baris 39-50):** dev menambah `x/oauth2 v0.36.0` (dengan `sync`/`sys` versi lama); main menambah `goldmark` + bump `sync`/`sys`.
- **Resolusi:** pertahankan `go-oidc/v3 v3.20.0` dan `x/oauth2 v0.36.0`, ambil SEMUA versi dari main (`chi v5.3.0`, `goldmark v1.8.4`, `sync v0.22.0`, `sys v0.47.0`), lalu jalankan `go mod tidy` untuk merapikan indirect.

### A10. `server/go.sum` — 1 zona

- **Zona konflik (~baris 174-207):** blok checksum `golang.org/x/*` — sisi dev berisi versi lama (`net v0.43.0`, `sync v0.20.0`, `sys v0.35.0`, `text v0.35.0`) + `oauth2 v0.36.0`; sisi main berisi versi baru (`mod v0.38.0`, `net v0.57.0`, `sync v0.22.0`, `sys v0.47.0`, `telemetry`, `text v0.40.0`).
- **Resolusi:** JANGAN hand-merge checksum. Ambil sisi main apa adanya, lalu setelah `go.mod` di-resolve jalankan `go mod tidy` — checksum `go-oidc`/`oauth2`/`go-jose` akan di-regenerate otomatis. Verifikasi dengan `go build ./...` dan `make test`.

---

## B. Berubah di Kedua Sisi, Auto-merge Bersih (15)

Tetap perlu dicek ulang setelah merge (semantic conflict tidak terdeteksi oleh git):

- `apps/docs/app/global.css`
- `apps/web/app/(auth)/login/page.tsx`
- `apps/web/app/(landing)/layout.tsx`
- `apps/web/app/custom.css`
- `apps/web/app/not-found.tsx`
- `packages/core/api/schemas.ts`
- `packages/ui/styles/tokens.css`
- `packages/views/auth/login-page.tsx`
- `packages/views/locales/en/auth.json`
- `packages/views/locales/en/common.json`
- `packages/views/locales/ja/auth.json`
- `packages/views/locales/ko/auth.json`
- `packages/views/locales/zh-Hans/auth.json`
- `server/internal/handler/auth.go`
- `server/internal/handler/config.go`

---

## C. Hanya Ada/Diubah di `dev_taskor` (32) — aman, tidak akan konflik

Termasuk seluruh file baru yang tidak pernah ada di upstream:

- **Backend SSO baru:** `server/internal/handler/sso.go`, `server/internal/sso/oidc.go`, `server/internal/sso/state.go`
- **Rebrand & tema:** `apps/web/app/(landing)/*` (about, changelog, contact-sales, download, homepage, page), `apps/web/app/favicon.ico/route.ts`, `apps/web/public/favicon.svg`, `packages/ui/components/common/multica-icon.tsx`
- **Dokumentasi:** seluruh `support_docs/**` (SSO plans, installation, migration, PRD, custom-ui, logo)

> Catatan: `support_docs/` tidak ada di `origin/main`, jadi folder ini akan **ditambahkan** ke main saat merge tanpa konflik.

---

## Rekomendasi Strategi Merge

1. **Backup sudah aman** — `dev_taskor_bak` (lokal + remote).
2. **Disarankan: merge `main` → `dev_taskor` dulu** (bukan langsung `dev_taskor` → `main`):
   ```bash
   git checkout dev_taskor
   git merge origin/main
   # resolve 10 konflik di sini, test, baru lanjut merge ke main
   ```
   Keuntungan: konflik diselesaikan di branch kerja, `main` tetap bersih, dan hasilnya bisa dites dulu.
3. **Urutan resolusi yang disarankan** (detail per file + snippet hasil gabungan ada di bagian A1-A10):
   - `go.mod`/`go.sum`: terima versi upstream, lalu tambahkan kembali `go-oidc/v3` + `oauth2`, jalankan `go mod tidy`
   - `router.go` + `handler.go`: gabungkan — pertahankan route/wiring SSO di atas struktur upstream terbaru
   - `config/index.ts` + `.env.example`: pertahankan variabel `MULTICA_SSO_*` di atas versi upstream
   - `layout.tsx`: terapkan kembali rebrand title di atas layout upstream
   - Auth flow (`callback/page.tsx`, `auth-initializer.tsx`, `use-logout.ts`): paling riskan — cek ulang logika redirect Keycloak & SLO terhadap auth flow upstream terbaru
4. **Verifikasi pasca-merge** (wajib, terutama 15 file kategori B):
   ```bash
   pnpm install
   pnpm typecheck
   pnpm test
   make test
   ```

## Reproduksi Analisis

```bash
git fetch origin
git merge-base dev_taskor origin/main
git diff --name-only <base> dev_taskor      # perubahan sisi dev
git diff --name-only <base> origin/main     # perubahan sisi main
git merge-tree --write-tree --name-only dev_taskor origin/main   # konflik aktual
```
