# Testing Guide — Batch 2: SSO Core Package

**Date:** 2026-07-17
**Reference:** `support_docs/SSO/multica/development_plan_keycloak_sso_phased.md` (Batch 2)
**Scope:** Verifikasi bahwa package `server/internal/sso/` (OIDC client + state cookie) kompilasi, dependensi promote ke direct, dan OIDC discovery ke Keycloak Larasati berhasil. **Tidak ada HTTP route SSO** di batch ini — itu Batch 3.

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
- **Project root:** `/home/multica/multica`
- **Server module:** `/home/multica/multica/server`

---

## Perubahan yang Diuji

| File | Perubahan |
|------|-----------|
| `server/internal/sso/oidc.go` | NEW — `OIDCClient`, `NewOIDCClient` (discovery), `AuthURL` (PKCE S256), `ExchangeCode` (token exchange + email), `GenerateCodeVerifier`, `GenerateState` |
| `server/internal/sso/state.go` | NEW — State cookie `multica_sso_state` (`SameSite=Lax`, HttpOnly, HMAC-SHA256 signed dengan `JWT_SECRET`) |
| `server/internal/handler/handler.go` | MODIFY — field `OIDC *sso.OIDCClient` di struct `Handler` + import `sso` |
| `server/cmd/server/router.go` | MODIFY — import `sso` + init `sso.NewOIDCClient` setelah `handler.New(...)` jika `SSOEnabled` |
| `server/go.mod` / `go.sum` | MODIFY — `go-oidc/v3` + `oauth2` promote ke **direct dependency** (bukan indirect lagi) |

---

## Persiapan: Pull + Rebuild

```bash
cd /home/multica/multica

# 1. Restore go.mod/go.sum jika ada local modification
git checkout -- server/go.mod server/go.sum

# 2. Pull perubahan terbaru
git pull origin dev_taskor

# 3. Rebuild binary backend (WAJIB — service pakai binary)
cd server
go build -o bin/server ./cmd/server

# 4. Restart backend service
sudo systemctl restart multica-backend
sleep 2
```

> **Catatan:** Setelah Batch 2, `go mod tidy` **AMAN** dijalankan di server karena import `go-oidc/v3` sudah ada di `oidc.go`. Tidy akan promote ke direct dependency (bukan strip). Tapi tetap opsional — `go build` sudah cukup.

---

## Skenario Uji

### T1. Compile Check (wajib)

**Tujuan:** Pastikan package `sso` + perubahan handler/router kompilasi tanpa error.

```bash
cd /home/multica/multica/server
go build ./...
go vet ./...
```

**Ekspektasi:**
- `go build ./...` → tidak ada output (sukses)
- `go vet ./...` → tidak ada output

**Gagal jika:** ada error compile. Kemungkinan:
- Import `sso` salah path
- Field `OIDC` belum ditambah ke struct `Handler`
- API `go-oidc/v3` berubah (cek `go doc github.com/coreos/go-oidc/v3/oidc`)

---

### T2. Dependensi Promote ke Direct (wajib)

**Tujuan:** Pastikan `go-oidc/v3` dan `oauth2` sekarang **direct dependency** (bukan `// indirect`).

```bash
cd /home/multica/multica/server
grep "coreos/go-oidc" go.mod
grep "golang.org/x/oauth2" go.mod
```

**Ekspektasi:** keduanya muncul di blok `require` atas **tanpa** `// indirect`.

**Contoh output yang benar:**
```
	github.com/coreos/go-oidc/v3 v3.20.0
	golang.org/x/oauth2 v0.36.0
```

**Bukan ini (masih indirect):**
```
	github.com/coreos/go-oidc/v3 v3.20.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
```

**Gagal jika:** masih `// indirect`. Solusi: `go mod tidy` (sekarang aman karena import sudah ada).

---

### T3. Startup Log — OIDC Enabled (wajib)

**Tujuan:** Pastikan server start dengan log "sso: keycloak oidc enabled" saat `MULTICA_SSO_ENABLED=true`.

**Pastikan `.env` punya SSO config** (sudah diset di Batch 1):
```bash
grep MULTICA_SSO_ENABLED /home/multica/multica/.env
# Ekspektasi: MULTICA_SSO_ENABLED=true
```

Restart + cek log:
```bash
sudo systemctl restart multica-backend
sleep 2
sudo journalctl -u multica-backend --since "1 min ago" | grep "sso:"
```

**Ekspektasi:**
```
sso: keycloak oidc enabled issuer=https://larasati.lintasarta.co.id/realms/dev
```

**Gagal jika:**
- Tidak ada log "sso:" → `MULTICA_SSO_ENABLED` tidak terbaca, atau binary belum di-rebuild
- Log "sso: keycloak oidc init failed" → discovery ke Keycloak gagal (cek jaringan / issuer URL)

---

### T4. Startup Log — OIDC Disabled (wajib)

**Tujuan:** Pastikan server start **tanpa** log SSO saat `MULTICA_SSO_ENABLED=false`.

Edit `.env`:
```bash
nano /home/multica/multica/.env
# Ubah: MULTICA_SSO_ENABLED=false
```

Restart + cek log:
```bash
sudo systemctl restart multica-backend
sleep 2
sudo journalctl -u multica-backend --since "1 min ago" | grep "sso:"
```

**Ekspektasi:** tidak ada output (tidak ada log SSO).

**Setelah test, kembalikan ke true untuk batch selanjutnya:**
```bash
sed -i 's/MULTICA_SSO_ENABLED=false/MULTICA_SSO_ENABLED=true/' /home/multica/multica/.env
sudo systemctl restart multica-backend
```

---

### T5. OIDC Discovery Smoke Test (opsional, butuh akses jaringan)

**Tujuan:** Verifikasi `NewOIDCClient` berhasil discovery ke issuer Larasati. Sebenarnya sudah tercakup di T3 (jika startup log muncul, discovery sukses). Test ini untuk isolasi jika T3 gagal.

**Cara (Go scratch program):** buat file sementara `server/cmd/scratch/main.go`:
```go
package main

import (
    "context"
    "fmt"
    "github.com/multica-ai/multica/server/internal/sso"
)

func main() {
    client, err := sso.NewOIDCClient(context.Background(),
        "https://larasati.lintasarta.co.id/realms/dev",
        "task-or",
        "oV6mcQShpvBtogbTGgGkuzKZ09Fy1o3X",
        "http://localhost:3000/auth/keycloak/callback",
    )
    if err != nil {
        fmt.Println("FAIL:", err)
        return
    }
    // Generate PKCE + state, build auth URL
    verifier, _ := sso.GenerateCodeVerifier()
    state, _ := sso.GenerateState()
    authURL := client.AuthURL(state, verifier)
    fmt.Println("OK: discovery succeeded")
    fmt.Println("AuthURL:", authURL)
}
```

```bash
cd /home/multica/multica/server
go run ./cmd/scratch
```

**Ekspektasi:**
```
OK: discovery succeeded
AuthURL: https://larasati.lintasarta.co.id/realms/dev/protocol/openid-connect/auth?client_id=task-or&code_challenge=...&code_challenge_method=S256&...
```

**Gagal jika:** `FAIL: ...` — cek:
```bash
curl -v https://larasati.lintasarta.co.id/realms/dev/.well-known/openid-configuration
```

> **Hapus** `server/cmd/scratch/` setelah test — jangan commit.

---

### T6. State Cookie Round-Trip (opsional, via unit test)

**Tujuan:** Verifikasi `SetStateCookie` → `ReadStateCookie` round-trip + signature verification + expiry.

Buat `server/internal/sso/state_test.go`:
```go
package sso

import (
    "net/http"
    "net/http/httptest"
    "testing"
    "time"
)

func TestStateCookieRoundTrip(t *testing.T) {
    key := []byte("test-secret-key")
    payload := StatePayload{
        State:        "abc123",
        CodeVerifier: "verifier456",
        Next:         "/dashboard",
    }

    w := httptest.NewRecorder()
    if err := SetStateCookie(w, payload, key); err != nil {
        t.Fatal(err)
    }

    req := httptest.NewRequest("GET", "/", nil)
    req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))

    got, err := ReadStateCookie(req, key)
    if err != nil {
        t.Fatal(err)
    }
    if got.State != payload.State || got.CodeVerifier != payload.CodeVerifier || got.Next != payload.Next {
        t.Fatalf("payload mismatch: got %+v", got)
    }
}

func TestStateCookieTampered(t *testing.T) {
    key := []byte("test-secret-key")
    payload := StatePayload{State: "abc", CodeVerifier: "v"}

    w := httptest.NewRecorder()
    SetStateCookie(w, payload, key)

    // Tamper the cookie value
    cookie := w.Header().Get("Set-Cookie")
    tampered := cookie[:len(cookie)-2] + "XX"

    req := httptest.NewRequest("GET", "/", nil)
    req.Header.Set("Cookie", tampered)

    _, err := ReadStateCookie(req, key)
    if err == nil {
        t.Fatal("expected error for tampered cookie")
    }
}

func TestStateCookieExpired(t *testing.T) {
    key := []byte("test-secret-key")
    // Manually craft an expired payload
    payload := StatePayload{
        State:        "abc",
        CodeVerifier: "v",
        Exp:          time.Now().Add(-1 * time.Minute).Unix(),
    }

    w := httptest.NewRecorder()
    // Bypass SetStateCookie's auto-Exp by encoding manually
    body, _ := jsonMarshal(payload)
    encoded := base64RawURL(body)
    sig := hmacSHA256(encoded, key)
    http.SetCookie(w, &http.Cookie{
        Name:  CookieName,
        Value: encoded + "." + sig,
        Path:  "/",
    })

    req := httptest.NewRequest("GET", "/", nil)
    req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))

    _, err := ReadStateCookie(req, key)
    if err == nil {
        t.Fatal("expected error for expired cookie")
    }
}
```

> Catatan: `jsonMarshal` / `base64RawURL` di test atas perlu disesuaikan — atau sederhanakan dengan expose helper. Jika test terlalu rumit, skip T6 dan andalkan T3 (startup log) sebagai bukti discovery sukses.

```bash
cd /home/multica/multica/server
go test ./internal/sso/... -v
```

---

## Checklist Lolos / Gagal

| Skenario | Wajib | Status |
|----------|-------|--------|
| T1. Compile check (`go build` + `go vet`) | ✓ wajib | ☐ |
| T2. Dependensi promote ke direct (bukan indirect) | ✓ wajib | ☐ |
| T3. Startup log "sso: keycloak oidc enabled" saat aktif | ✓ wajib | ☐ |
| T4. Tidak ada log SSO saat disabled | ✓ wajib | ☐ |
| T5. OIDC discovery smoke test | opsional | ☐ |
| T6. State cookie round-trip unit test | opsional | ☐ |

**Batch 2 dinyatakan LOLOS jika T1–T4 semua lulus.** T5–T6 opsional untuk konfirmasi tambahan.

---

## Troubleshooting

| Gejala | Kemungkinan Penyebab | Solusi |
|--------|---------------------|--------|
| `go build` error: undefined `sso.OIDCClient` | Field `OIDC` belum ditambah ke `Handler`, atau import `sso` belum ditambah | Cek `handler.go` import + struct field; `router.go` import |
| `go build` error: undefined `oidc.S256ChallengeFromVerifier` | API `go-oidc/v3` berubah — helper PKCE ada di `oauth2`, bukan `oidc` | Pakai `oauth2.S256ChallengeOption(verifier)` |
| `go build` error: undefined `ctx` | Scope tidak punya `ctx` | Pakai `context.Background()` |
| Startup log: "sso: keycloak oidc init failed" | Discovery ke Keycloak gagal | `curl -v https://larasati.lintasarta.co.id/realms/dev/.well-known/openid-configuration` — cek jaringan, DNS, TLS cert |
| Tidak ada log "sso:" padahal `MULTICA_SSO_ENABLED=true` | Binary belum di-rebuild, atau env var tidak masuk proses | `go build -o bin/server ./cmd/server` + restart; cek `cat /proc/$(pgrep -f server/bin/server)/environ \| tr '\0' '\n' \| grep MULTICA_SSO` |
| Dependensi masih `// indirect` | `go mod tidy` belum dijalankan setelah import ada | `go mod tidy` (sekarang aman di Batch 2) |

---

## Setelah Lolos

Batch 2 selesai → lanjut ke **Batch 3: Backend Handler + Routes** (`server/internal/handler/sso.go` + route registration di `router.go`). Batch 3 akan menambah `KeycloakLogin` + `KeycloakCallback` handler dan daftarkan route `GET /auth/keycloak/login` + `GET /auth/keycloak/callback`.

Setelah Batch 3 di-push, di server jalankan:
```bash
cd /home/multica/multica
git pull origin dev_taskor
cd server
go build -o bin/server ./cmd/server
sudo systemctl restart multica-backend
# Test: curl -v http://localhost:3000/auth/keycloak/login → 302 ke Keycloak
```