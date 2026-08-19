# Panduan Update Server — Deploy Hasil Merge `dev_taskor` + `main`

Tanggal: 2026-08-18
Tujuan: langkah build, deploy, dan testing di server production untuk hasil merge `main` (upstream, 734 commit) ke dalam `dev_taskor` (SSO Keycloak + rebrand Super-Presales).

Konteks merge:

- Commit hasil merge: `552d85a4e` (`Merge branch 'main' into dev_taskor`)
- Kebijakan resolusi: fungsi SSO Keycloak + rebrand Super-Presales dipertahankan, sisanya mengikuti upstream
- Detail konflik & resolusi: lihat `support_docs/merge-analysis-dev_taskor-ke-main.md`
- Backup pre-merge: branch `dev_taskor_bak` (lokal + remote)

Asumsi deployment (sesuai `installation-bare-metal.md`):

- Path instalasi: `/opt/multica`
- Service systemd: `multica-backend` (Go binary) dan `multica-frontend` (Next.js standalone)
- PostgreSQL sudah berjalan (Docker atau native)
- `.env` existing sudah berisi konfigurasi SSO Keycloak (`MULTICA_SSO_*`)

> Sesuaikan path/nama service jika deployment kamu berbeda.

## Perubahan Requirement Penting

| Tool | Sebelum merge | Sesudah merge | Catatan |
|---|---|---|---|
| Go | 1.26.1 | **1.26.6** | `go.mod` dinaikkan upstream. Update Go, atau biarkan toolchain auto-download (default `GOTOOLCHAIN=auto`, butuh internet) |
| Node.js | ≥ 22 | ≥ 22 | Tidak berubah |
| pnpm | 10.28.2 | 10.28.2 | Tidak berubah |

## Estimasi Downtime

± 5-10 menit (Fase 4-6). Build (Fase 2-3) dilakukan SAAT services masih berjalan versi lama untuk meminimalkan downtime.

---

## Fase 0 — Backup (WAJIB, jangan skip)

```bash
# Catat commit yang sedang berjalan (titik rollback)
cd /opt/multica
git log --oneline -1   # CATAT hash ini

# Backup database (format custom, cepat restore, support pgvector)
set -a; source .env; set +a
pg_dump "$DATABASE_URL" -Fc -f /tmp/multica-pre-merge-$(date +%Y%m%d).dump

# Verifikasi backup berisi tabel-tabel utama
pg_restore --list /tmp/multica-pre-merge-$(date +%Y%m%d).dump | head -5

# Backup .env (berisi config SSO Keycloak)
cp .env /tmp/env-backup-$(date +%Y%m%d)
```

## Fase 1 — Update Kode

```bash
cd /opt/multica
git fetch origin
git checkout dev_taskor
git pull origin dev_taskor
git log --oneline -1
# Harus: 552d85a4e Merge branch 'main' into dev_taskor
```

## Fase 2 — Build Backend

```bash
# Cek versi Go — wajib >= 1.26.6
go version
# Jika < 1.26.6: update Go dulu, atau biarkan toolchain auto-download
# saat build (butuh akses internet ke proxy.golang.org)

cd /opt/multica/server
go mod download

go build -ldflags "-s -w" -o bin/server ./cmd/server
go build -ldflags "-s -w" -o bin/multica ./cmd/multica
go build -ldflags "-s -w" -o bin/migrate ./cmd/migrate

# Verifikasi 3 binary ter-update
ls -la bin/
```

## Fase 3 — Build Frontend

```bash
cd /opt/multica

# WAJIB — 734 commit upstream mengubah banyak dependency
pnpm install

# Build semua package (core/ui/views) + web app (output standalone)
pnpm build
```

## Fase 4 — Database Migration (Mulai Downtime)

734 commit upstream membawa **banyak migration baru**. Langkah ini krusial:

```bash
cd /opt/multica
set -a; source .env; set +a
./server/bin/migrate up
```

Jika migration gagal → stop di sini, restore dari dump Fase 0 (lihat bagian Rollback), dan investigasi error-nya sebelum lanjut.

## Fase 5 — Restart Services

```bash
sudo systemctl restart multica-backend
sudo systemctl restart multica-frontend
sudo systemctl status multica-backend multica-frontend

# Pantau log backend — cari baris inisialisasi SSO:
sudo journalctl -u multica-backend -f
```

Indikator sukses di log:

```json
"sso: keycloak oidc enabled" issuer=https://larasati.lintasarta.co.id/realms/dev
```

> Catatan: jika SSO gagal init (issuer salah / secret salah / TLS gagal), backend **sengaja exit(1)** by design. Perbaiki `.env` lalu restart lagi.

## Fase 6 — Checklist Testing

### 6.1 Smoke test (dari server)

```bash
curl http://localhost:8080/healthz
# Harus: {"status":"ok"}

curl http://localhost:8080/readyz
# Harus: {"status":"ok","checks":{"db":"ok","migrations":"ok"}}

curl -s http://localhost:8080/api/config | grep -o '"sso_enabled":[^,]*'
# Harus: "sso_enabled":true
```

### 6.2 Test SSO (browser) — prioritas utama

- [ ] Buka `https://<domain>` → tab browser bertitle **"Super-Presales"**, favicon baru tampil
- [ ] Halaman login menampilkan tombol **"Login with Keycloak"**
- [ ] Klik tombol → redirect ke Keycloak Larasati → login → callback → masuk dashboard
- [ ] **Test SLO (Single Logout)**: logout → dialihkan lewat Keycloak `end_session` → kembali ke `/login`
- [ ] Klik "Login with Keycloak" lagi → harus **minta kredensial ulang** (bukan auto-login). Jika auto-login tanpa tanya password, SLO belum bekerja

### 6.3 Test fungsi inti

- [ ] Login via email masih jalan (bypass login aktif — kode apa pun diterima, sesuai konfigurasi saat ini)
- [ ] Buka workspace → daftar issues tampil → buka 1 issue → tambah komentar
- [ ] Switch workspace berfungsi
- [ ] Cek `sudo journalctl -u multica-backend` — tidak ada error berulang
  - Error seputar DingTalk/WeCom/VCS boleh diabaikan: fitur baru upstream, disabled by default (tidak ada env-nya)

### 6.4 Hal yang TIDAK perlu diubah

- Config di sisi Keycloak server (client ID, secret, Valid Redirect URIs) — semua sama
- `JWT_SECRET` di `.env` — jangan di-regenerate (session user yang aktif tetap valid)
- Variabel env baru upstream (`MULTICA_WECOM_*`, dll) — opsional, abaikan jika tidak dipakai

## Rollback (Jika Ada Masalah)

```bash
sudo systemctl stop multica-backend multica-frontend

cd /opt/multica
git checkout <hash-dari-Fase-0>

# Rebuild backend lama
cd server
go mod download
go build -ldflags "-s -w" -o bin/server ./cmd/server
cd ..

# Restore database ke kondisi pre-merge
set -a; source .env; set +a
pg_restore -d "$DATABASE_URL" --clean /tmp/multica-pre-merge-*.dump

# Rebuild frontend lama
pnpm install
pnpm build

sudo systemctl start multica-backend multica-frontend
```

## Known Issues (tidak blocking, dari verifikasi merge)

1. **String fitur baru upstream masih berbahasa "Multica"** — DingTalk, WeCom, dan fitur baru lain membawa branding Multica di locale strings. Sweep rebrand menyusul jika diperlukan.
2. **Landing pages** — versi rebrand lama koeksist dengan struktur landing baru upstream; cek visual jika landing page dipakai.
3. **Test guardrail upstream** (`type-scale`, `text-contrast`) sudah merah di upstream sendiri pada environment Windows — bukan regresi merge. Kode milik dev sudah patuh token.
4. **Go test yang butuh infra** (postgres/redis/docker, path Unix) gagal di Windows lokal — verifikasi final via CI Linux atau langsung di server.

## Dokumen Terkait

- `support_docs/merge-analysis-dev_taskor-ke-main.md` — analisis konflik & resolusi per file
- `support_docs/installation-bare-metal.md` — instalasi dari nol
- `support_docs/migration-server.md` — migrasi antar server
- `support_docs/SSO/multica/` — rencana implementasi & testing guide SSO per batch
