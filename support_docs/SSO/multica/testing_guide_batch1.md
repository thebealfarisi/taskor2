# Testing Guide — Batch 1: Dependencies + Config

**Date:** 2026-07-17 (updated with actual deployment conditions)
**Reference:** `support_docs/SSO/multica/development_plan_keycloak_sso_phased.md` (Batch 1)
**Scope:** Verifikasi bahwa dependensi OIDC terpasang, field config SSO terbaca dari env, dan endpoint `/api/config` mengembalikan flag `sso_enabled` dengan benar. **Tidak ada flow OIDC** di batch ini — itu Batch 3.

---

## Environment

| Aktivitas | Lokasi | Shell |
|-----------|--------|-------|
| **Development** (edit kode, commit, push) | Windows: `D:\Kerjaan\Project\taskor2` | PowerShell |
| **Testing** (build, run, verify) | Ubuntu server: `/home/multica/multica` | Bash |

### Deployment Topology (server Ubuntu)

- **Backend:** systemd service `multica-backend`
  - `ExecStart=/home/multica/multica/server/bin/server` (binary, bukan `go run`)
  - `EnvironmentFile=/home/multica/multica/.env`
  - Restart: `sudo systemctl restart multica-backend`
- **Frontend:** systemd service `multica-frontend`
  - Restart: `sudo systemctl restart multica-frontend`
- **Project root:** `/home/multica/multica`
- **Server module:** `/home/multica/multica/server`

> **Workflow:** edit kode di Windows → `git push` → `git pull` di server → **rebuild binary** → restart systemd service → test via `curl`.

---

## Prasyarat (di server Ubuntu)

- Go 1.26+ terinstall (`go version`)
- Project sudah ter-clone di `/home/multica/multica`
- Akses ke Keycloak issuer `https://larasati.lintasarta.co.id/realms/dev` (opsional, hanya untuk smoke test T7)
- systemd services `multica-backend` + `multica-frontend` sudah ter-config dan jalan

## Perubahan yang Diuji

| File | Perubahan |
|------|-----------|
| `server/go.mod` / `go.sum` | `github.com/coreos/go-oidc/v3 v3.20.0`, `golang.org/x/oauth2 v0.36.0` |
| `server/internal/handler/handler.go` | 5 field SSO di struct `Config` |
| `server/cmd/server/router.go` | Baca env `MULTICA_SSO_*` ke `signupConfig` |
| `server/internal/handler/config.go` | Field `SSOEnabled` di `AppConfig` + `GetConfig` |
| `.env.example` | Blok dokumentasi SSO |

---

## Persiapan: Pull + Rebuild

Sebelum menjalankan skenario test, lakukan langkah ini di server:

```bash
cd /home/multica/multica

# 1. Pull perubahan terbaru dari remote
git pull origin dev_taskor

# 2. Restore go.mod/go.sum jika ada local modification (sisa go mod tidy sebelumnya)
git checkout -- server/go.mod server/go.sum

# 3. Rebuild binary backend (WAJIB — service pakai binary, bukan go run)
cd server
go build -o bin/server ./cmd/server

# 4. Restart backend service
sudo systemctl restart multica-backend
sleep 2
```

> **⚠ JANGALAH jalankan `go mod tidy` di server selama Batch 1.** `go mod tidy` akan **menghapus** dependency indirect yang tidak di-import kode mana pun (import `go-oidc/v3` baru ada di Batch 2), sehingga `go list -m` akan kembali "not a known dependency". Cukup `git pull` + `go build`. `go mod tidy` aman dijalankan lagi setelah Batch 2 selesai.

---

## Skenario Uji

### T1. Compile Check (wajib)

**Tujuan:** Pastikan semua perubahan Batch 1 kompilasi tanpa error.

```bash
cd /home/multica/multica/server
go build ./...
go vet ./...
```

**Ekspektasi:**
- `go build ./...` → tidak ada output (sukses)
- `go vet ./...` → tidak ada output (tidak ada warning)

**Gagal jika:** ada error compile atau warning vet. Kemungkinan penyebab:
- Field struct typo / tidak konsisten
- Import yang tidak terpakai
- `go mod tidy` belum dijalankan di Windows (development), atau justru dijalankan di server (strip dep)

---

### T2. Dependensi Terpasang (wajib)

**Tujuan:** Pastikan `go-oidc/v3` dan `oauth2` ada di `go.mod`.

```bash
cd /home/multica/multica/server
go list -m github.com/coreos/go-oidc/v3
go list -m golang.org/x/oauth2
```

**Ekspektasi:**
- `github.com/coreos/go-oidc/v3 v3.20.0` (atau lebih baru)
- `golang.org/x/oauth2 v0.36.0` (atau lebih baru)

**Verifikasi di go.mod:**
```bash
grep "coreos/go-oidc" go.mod
grep "golang.org/x/oauth2" go.mod
```

**Ekspektasi:** keduanya muncul di go.mod.

> **Catatan `// indirect`:** Di Batch 1, kedua package akan muncul sebagai `// indirect` karena **belum ada kode Go yang import** mereka (import baru ada di Batch 2 saat `server/internal/sso/oidc.go` dibuat). Mereka akan otomatis menjadi **direct dependency** di Batch 2 saat import statement ditambahkan. Jadi di Batch 1, `// indirect` **bukan kegagalan** — yang penting package ter-download dan tercatat di go.mod.

---

### T3. Config Endpoint — SSO Enabled (wajib)

**Tujuan:** Pastikan `GET /api/config` mengembalikan `"sso_enabled": true` saat env SSO aktif.

**Setup env** — edit `/home/multica/multica/.env` (file yang dibaca systemd via `EnvironmentFile`):

```bash
nano /home/multica/multica/.env
```

Tambahkan (atau pastikan ada) blok berikut:

```bash
# Keycloak SSO
MULTICA_SSO_ENABLED=true
MULTICA_SSO_KEYCLOAK_ISSUER=https://larasati.lintasarta.co.id/realms/dev
MULTICA_SSO_CLIENT_ID=task-or
MULTICA_SSO_CLIENT_SECRET=oV6mcQShpvBtogbTGgGkuzKZ09Fy1o3X
MULTICA_SSO_REDIRECT_URL=http://localhost:3000/auth/keycloak/callback
```

> **Format `.env` systemd:** `KEY=VALUE` per baris, **tanpa** `export`, **tanpa** quotes (kecuali value mengandung spasi), **tanpa** spasi di sekitar `=`.

Restart backend lalu test:

```bash
sudo systemctl restart multica-backend
sleep 2
curl -s http://localhost:3000/api/config | python3 -m json.tool
```

**Ekspektasi:** response JSON mengandung:
```json
{
  "allow_signup": false,
  "sso_enabled": true,
  ...
}
```

**Verifikasi spesifik:**
```bash
curl -s http://localhost:3000/api/config | grep "sso_enabled"
```
**Ekspektasi:** `"sso_enabled": true`

**Debug jika `sso_enabled` tidak muncul:**

1. **Cek env var masuk ke proses server:**
   ```bash
   PID=$(pgrep -f "server/bin/server")
   cat /proc/$PID/environ | tr '\0' '\n' | grep MULTICA_SSO
   ```
   Jika kosong → `.env` tidak terbaca systemd. Cek path `EnvironmentFile` di `systemctl cat multica-backend`.

2. **Cek binary sudah di-rebuild:**
   ```bash
   ls -la /home/multica/multica/server/bin/server
   ```
   Timestamp harus sesudah `git pull`. Jika binary lama, rebuild:
   ```bash
   cd /home/multica/multica/server
   go build -o bin/server ./cmd/server
   sudo systemctl restart multica-backend
   ```
   Binary lama tidak punya field `SSOEnabled` di `AppConfig`, jadi walau env var terbaca, `GetConfig` tidak mengembalikannya.

3. **Cek `.env` terbaca:**
   ```bash
   grep MULTICA_SSO /home/multica/multica/.env
   ```

---

### T4. Config Endpoint — SSO Disabled (wajib)

**Tujuan:** Pastikan `GET /api/config` **tidak** mengembalikan field `sso_enabled` saat SSO dimatikan (omitempty).

**Setup env** — edit `/home/multica/multica/.env`:

```bash
nano /home/multica/multica/.env
```

Ubah `MULTICA_SSO_ENABLED=true` menjadi `false` (atau hapus barisnya):

```bash
MULTICA_SSO_ENABLED=false
```

Restart backend lalu test:

```bash
sudo systemctl restart multica-backend
sleep 2
curl -s http://localhost:3000/api/config | python3 -m json.tool
```

**Ekspektasi:** response JSON **tidak mengandung** field `sso_enabled` (karena `omitempty`).

**Verifikasi:**
```bash
curl -s http://localhost:3000/api/config | grep "sso_enabled"
```
**Ekspektasi:** tidak ada output (field tidak ada di response).

---

### T5. Config Struct Field Terbaca (opsional)

**Tujuan:** Pastikan env vars `MULTICA_SSO_*` terbaca ke struct `Config` (bukan hanya ke `AppConfig`). Batch 1 belum init OIDC client (itu Batch 2), jadi tidak ada log "sso: keycloak oidc enabled" — itu baru muncul Batch 2.

**Cara verifikasi:** cek bahwa server start tanpa panic saat env SSO aktif. Jika field struct tidak terbaca, tidak akan ada dampak di Batch 1 (karena belum ada yang konsumsi field tersebut). Verifikasi penuh terjadi di Batch 2 saat `NewOIDCClient` dipanggil dengan field-field ini.

---

### T6. Env Example Documentation (opsional)

**Tujuan:** Pastikan `.env.example` punya blok dokumentasi SSO.

```bash
grep "MULTICA_SSO_ENABLED" /home/multica/multica/.env.example
```

**Ekspektasi:** muncul, dengan komentar penjelasan.

---

### T7. Smoke Test OIDC Discovery (opsional, butuh akses jaringan)

**Tujuan:** Verifikasi bahwa dependensi `go-oidc` bisa melakukan discovery ke issuer Larasati. **Ini bukan test Batch 1 secara teknis** (discovery dipanggil di Batch 2), tapi berguna untuk konfirmasi dini bahwa jaringan + issuer URL benar sebelum Batch 2.

**Cara (Go scratch program):** buat file sementara `server/cmd/scratch/main.go`:
```go
package main

import (
    "context"
    "fmt"
    "github.com/coreos/go-oidc/v3/oidc"
)

func main() {
    _, err := oidc.NewProvider(context.Background(), "https://larasati.lintasarta.co.id/realms/dev")
    if err != nil {
        fmt.Println("FAIL:", err)
        return
    }
    fmt.Println("OK: discovery succeeded")
}
```

```bash
cd /home/multica/multica/server
go run ./cmd/scratch
```

**Ekspektasi:** `OK: discovery succeeded`

**Gagal jika:** `FAIL: ...` — kemungkinan:
- Tidak ada akses jaringan ke `larasati.lintasarta.co.id`
  ```bash
  curl -v https://larasati.lintasarta.co.id/realms/dev/.well-known/openid-configuration
  ```
- Issuer URL salah (cek realm path)
- Cert TLS tidak terpercaya dari server ini

> **Hapus** `server/cmd/scratch/` setelah test — jangan commit.

---

## Checklist Lolos / Gagal

| Skenario | Wajib | Status |
|----------|-------|--------|
| T1. Compile check (`go build` + `go vet`) | ✓ wajib | ☐ |
| T2. Dependensi terpasang di go.mod | ✓ wajib | ☐ |
| T3. `/api/config` → `"sso_enabled": true` saat aktif | ✓ wajib | ☐ |
| T4. `/api/config` → tidak ada `sso_enabled` saat disabled | ✓ wajib | ☐ |
| T5. Config struct field terbaca | opsional | ☐ |
| T6. `.env.example` dokumentasi | opsional | ☐ |
| T7. OIDC discovery smoke test | opsional | ☐ |

**Batch 1 dinyatakan LOLOS jika T1–T4 semua lulus.** T5–T7 opsional untuk konfirmasi tambahan.

---

## Troubleshooting

| Gejala | Kemungkinan Penyebab | Solusi |
|--------|---------------------|--------|
| `go build` error: undefined `SSOEnabled` | Field belum ditambah ke `Config` atau `AppConfig` | Cek `handler.go` struct `Config` + `config.go` struct `AppConfig` |
| `go list -m` bilang "not a known dependency" | `go mod tidy` dijalankan di server → strip dep indirect | `git checkout -- server/go.mod server/go.sum` lalu `go build` (jangan `go mod tidy`) |
| `/api/config` tidak ada `sso_enabled` padahal env set | Binary belum di-rebuild | `cd server && go build -o bin/server ./cmd/server && sudo systemctl restart multica-backend` |
| `/api/config` tidak ada `sso_enabled`, binary sudah baru | Env var tidak masuk ke proses server | Cek `cat /proc/$(pgrep -f server/bin/server)/environ \| tr '\0' '\n' \| grep MULTICA_SSO`; pastikan `.env` terbaca systemd |
| `sso_enabled` muncul saat disabled | `omitempty` tag salah | Cek tag struct: `json:"sso_enabled,omitempty"` |
| T7 gagal: connection refused / timeout | Tidak ada akses ke `larasati.lintasarta.co.id` | `curl -v https://larasati.lintasarta.co.id/realms/dev/.well-known/openid-configuration` |
| Server tidak start setelah env SSO set | Env var format salah di `.env` | Pastikan `KEY=VALUE` tanpa `export`, tanpa quotes, tanpa spasi sekitar `=` |

---

## Setelah Lolos

Batch 1 selesai → lanjut ke **Batch 2: SSO Core Package** (`server/internal/sso/oidc.go` + `state.go`). Batch 2 akan menambahkan field `Handler.OIDC` + init `sso.NewOIDCClient` di router (yang ditunda dari Batch 1 agar compile tetap valid).

Setelah Batch 2 di-push, di server jalankan:
```bash
cd /home/multica/multica
git pull origin dev_taskor
cd server
go build -o bin/server ./cmd/server   # go mod tidy AMAN dijalankan setelah ini
sudo systemctl restart multica-backend
```