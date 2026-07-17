# Testing Guide — Batch 1: Dependencies + Config

**Date:** 2026-07-17
**Reference:** `support_docs/SSO/multica/development_plan_keycloak_sso_phased.md` (Batch 1)
**Scope:** Verifikasi bahwa dependensi OIDC terpasang, field config SSO terbaca dari env, dan endpoint `/api/config` mengembalikan flag `sso_enabled` dengan benar. **Tidak ada flow OIDC** di batch ini — itu Batch 3.

---

## Environment

| Aktivitas | Lokasi | Shell |
|-----------|--------|-------|
| **Development** (edit kode, commit) | Windows: `D:\Kerjaan\Project\taskor2` | PowerShell |
| **Testing** (build, run, verify) | Ubuntu server: `/home/multica/multica` | Bash |

> Workflow: edit kode di Windows → push/commit → pull di server Ubuntu → build + test di server. Semua command di bawah ini dijalankan **di server Ubuntu** kecuali dinyatakan lain.

---

## Prasyarat (di server Ubuntu)

- Go 1.26+ terinstall (`go version`)
- Project sudah ter-clone / ter-deploy di `/home/multica/multica`
- Akses ke Keycloak issuer `https://larasati.lintasarta.co.id/realms/dev` (opsional, hanya untuk smoke test discovery — tidak wajib untuk Batch 1)
- Server Multica bisa di-build sebelum perubahan (baseline)

## Perubahan yang Diuji

| File | Perubahan |
|------|-----------|
| `server/go.mod` / `go.sum` | `github.com/coreos/go-oidc/v3 v3.20.0`, `golang.org/x/oauth2 v0.36.0` |
| `server/internal/handler/handler.go` | 5 field SSO di struct `Config` |
| `server/cmd/server/router.go` | Baca env `MULTICA_SSO_*` ke `signupConfig` |
| `server/internal/handler/config.go` | Field `SSOEnabled` di `AppConfig` + `GetConfig` |
| `.env.example` | Blok dokumentasi SSO |

---

## Skenario Uji

> Semua command dijalankan di server Ubuntu via SSH, kecuali dinyatakan lain.

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
- `go mod tidy` belum dijalankan

---

### T2. Dependensi Terpasang (wajib)

**Tujuan:** Pastikan `go-oidc/v3` dan `oauth2` ada di `go.mod` sebagai direct dependency.

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

**Ekspektasi:** keduanya muncul **tanpa** `// indirect` (direct dependency).

**Gagal jika:** package tidak ditemukan atau masih `// indirect`. Solusi: `go mod tidy`.

---

### T3. Config Endpoint — SSO Enabled (wajib)

**Tujuan:** Pastikan `GET /api/config` mengembalikan `"sso_enabled": true` saat env SSO aktif.

**Setup env** (sesuaikan cara env loading di deployment Anda — `.env`, systemd EnvironmentFile, atau export langsung):

```bash
# Jika pakai .env file:
export MULTICA_SSO_ENABLED=true
export MULTICA_SSO_KEYCLOAK_ISSUER=https://larasati.lintasarta.co.id/realms/dev
export MULTICA_SSO_CLIENT_ID=task-or
export MULTICA_SSO_CLIENT_SECRET=oV6mcQShpvBtogbTGgGkuzKZ09Fy1o3X
export MULTICA_SSO_REDIRECT_URL=http://localhost:3000/auth/keycloak/callback
```

**Jalankan server** (sesuai setup deployment — systemd service, `make dev`, atau `go run ./cmd/server`), lalu:

```bash
curl -s http://localhost:3000/api/config | python3 -m json.tool
```

**Ekspektasi:** response JSON mengandung:
```json
{
  "allow_signup": true,
  "sso_enabled": true,
  ...
}
```

**Verifikasi spesifik:**
```bash
curl -s http://localhost:3000/api/config | grep "sso_enabled"
```
**Ekspektasi:** `"sso_enabled": true`

---

### T4. Config Endpoint — SSO Disabled (wajib)

**Tujuan:** Pastikan `GET /api/config` **tidak** mengembalikan field `sso_enabled` saat SSO dimatikan (omitempty).

**Setup env:**
```bash
# Unset atau set false
unset MULTICA_SSO_ENABLED
# atau: export MULTICA_SSO_ENABLED=false
```

**Restart server**, lalu:

```bash
curl -s http://localhost:3000/api/config | python3 -m json.tool
```

**Ekspektasi:** response JSON **tidak mengandung** field `sso_enabled` (karena `omitempty`).

**Verifikasi:**
```bash
curl -s http://localhost:3000/api/config | grep "sso_enabled"
```
**Ekspektasi:** tidak ada output (field tidak ada di response).

---

### T5. Config Struct Field Terbaca (verifikasi via log startup — opsional)

**Tujuan:** Pastikan env vars `MULTICA_SSO_*` terbaca ke struct `Config` (bukan hanya ke `AppConfig`). Batch 1 belum init OIDC client (itu Batch 2), jadi tidak ada log "sso: keycloak oidc enabled" — itu baru muncul Batch 2.

**Cara verifikasi (debug):** tambahkan sementara log di `router.go` setelah `signupConfig` literal, atau gunakan Delve debugger untuk inspect `signupConfig.SSOEnabled` / `signupConfig.SSOIssuer`.

**Alternatif tanpa kode:** cek bahwa server start tanpa panic saat env SSO aktif. Jika field struct tidak terbaca, tidak akan ada dampak di Batch 1 (karena belum ada yang konsumsi field tersebut). Verifikasi penuh terjadi di Batch 2 saat `NewOIDCClient` dipanggil dengan field-field ini.

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
- Tidak ada akses jaringan ke `larasati.lintasarta.co.id` (cek dari server: `curl -s https://larasati.lintasarta.co.id/realms/dev/.well-known/openid-configuration | head`)
- Issuer URL salah (cek realm path)
- Cert TLS tidak terpercaya dari server ini

> **Hapus** `server/cmd/scratch/` setelah test — jangan commit.

---

## Checklist Lolos / Gagal

| Skenario | Wajib | Status |
|----------|-------|--------|
| T1. Compile check (`go build` + `go vet`) | ✓ wajib | ☐ |
| T2. Dependensi terpasang (direct, bukan indirect) | ✓ wajib | ☐ |
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
| `/api/config` tidak ada `sso_enabled` padahal env set | Server belum restart setelah set env, atau env var tidak ter-load | Restart server; cek dengan `echo $MULTICA_SSO_ENABLED` |
| `go list` tidak menemukan package | `go mod tidy` belum jalan | `cd /home/multica/multica/server && go mod tidy` |
| `sso_enabled` muncul saat disabled | `omitempty` tag salah | Cek tag struct: `json:"sso_enabled,omitempty"` |
| T7 gagal: connection refused / timeout | Tidak ada akses ke `larasati.lintasarta.co.id` | Cek dari server: `curl -v https://larasati.lintasarta.co.id/realms/dev/.well-known/openid-configuration` |
| Server tidak start setelah env SSO set | Env var format salah / konflik | Cek log: `journalctl -u multica -f` (jika systemd) atau stdout |

---

## Setelah Lolos

Batch 1 selesai → lanjut ke **Batch 2: SSO Core Package** (`server/internal/sso/oidc.go` + `state.go`). Batch 2 akan menambahkan field `Handler.OIDC` + init `sso.NewOIDCClient` di router (yang ditunda dari Batch 1 agar compile tetap valid).