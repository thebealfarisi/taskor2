# Panduan Instalasi Multica — Bare Metal / Build from Source

> **Untuk yang ingin customisasi.** Panduan ini mengajarkan cara install Multica
> **tanpa Docker** (kecuali untuk database) — build backend dan frontend dari source
> code, lalu jalankan sebagai systemd service. Cocok untuk yang ingin mengubah
> tampilan UI dan logika backend.

---

## Kenapa Bare Metal, Bukan Docker?

| Aspek | Docker (`make selfhost`) | Bare Metal (panduan ini) |
|-------|--------------------------|--------------------------|
| Setup | Satu perintah, otomatis | Manual, banyak langkah |
| Customisasi UI | Harus rebuild Docker image | Edit → rebuild → restart |
| Customisasi Backend | Harus rebuild Docker image | Edit → `go build` → restart |
| Hot Reload Dev | Tidak ada | `pnpm dev:web` + `go run` |
| Dependency | Hanya Docker | Go, Node.js, pnpm, PostgreSQL |
| Resource | Lebih berat (container overhead) | Lebih ringan |
| Production | Direkomendasikan | Butuh konfigurasi manual |

**Panduan ini cocok jika kamu:**
- Ingin mengubah tampilan UI (warna, layout, komponen)
- Ingin mengubah logika backend (API, handler, service)
- Ingin development dengan hot reload
- Ingin kontrol penuh atas konfigurasi

---

## Apa Itu Multica? (Singkat)

Multica = platform manajemen tugas AI-native. Agent AI adalah anggota tim first-class.

```
┌──────────────┐     ┌──────────────┐     ┌──────────────────┐
│   Next.js    │────>│  Go Backend  │────>│   PostgreSQL     │
│   Frontend   │<────│  (Chi + WS)  │<────│   (pgvector)     │
│  (port 3000) │     │  (port 8080) │     │   (port 5432)    │
└──────────────┘     └──────┬───────┘     └──────────────────┘
                            │
                     ┌──────┴───────┐
                     │ Agent Daemon │  di mesin user
                     └──────────────┘  (claude, codex, copilot, dll.)
```

| Komponen | Teknologi | Binary? |
|----------|-----------|---------|
| Backend | Go 1.26.1 (Chi, sqlc, gorilla/websocket) | Ya — `server/bin/server` |
| Frontend | Next.js 16 (App Router, React 19) | Tidak — butuh Node.js runtime |
| Database | PostgreSQL 17 + pgvector | Install terpisah |
| CLI/Daemon | Go binary | Ya — `server/bin/multica` |
| Migration | Go binary | Ya — `server/bin/migrate` |

> ⚠️ **Penting**: Berbeda dari GoClaw yang satu binary embed Web UI,
> frontend Multica adalah Next.js app yang **butuh Node.js runtime**.
> Tidak bisa di-embed ke binary Go.

---

## Daftar Isi

1. [Requirements](#1-requirements)
2. [Install Prerequisites](#2-install-prerequisites)
3. [Clone Project](#3-clone-project)
4. [Setup Database PostgreSQL](#4-setup-database-postgresql)
5. [Konfigurasi Environment](#5-konfigurasi-environment)
6. [Build Backend dari Source](#6-build-backend-dari-source)
7. [Build Frontend dari Source](#7-build-frontend-dari-source)
8. [Jalankan Database Migration](#8-jalankan-database-migration)
9. [Jalankan Multica](#9-jalankan-multica)
10. [Setup systemd Service (Production)](#10-setup-systemd-service-production)
11. [Install CLI & Start Daemon](#11-install-cli--start-daemon)
12. [Login & First-Time Setup](#12-login--first-time-setup)
13. [Workflow Customisasi UI](#13-workflow-customisasi-ui)
14. [Workflow Customisasi Backend](#14-workflow-customisasi-backend)
15. [Development Mode (Hot Reload)](#15-development-mode-hot-reload)
16. [Operasi Sehari-hari](#16-operasi-sehari-hari)
17. [Troubleshooting](#17-troubleshooting)

---

## 1. Requirements

### Spesifikasi Server Minimum

| Komponen | Minimum | Rekomendasi |
|----------|---------|-------------|
| OS | Ubuntu 22.04 / Debian 12 | Ubuntu 22.04 LTS |
| RAM | 2 GB | 4 GB+ |
| CPU | 2 core | 4 core |
| Storage | 20 GB | 50 GB SSD |

### Software yang Dibutuhkan

| Software | Versi | Fungsi |
|----------|-------|--------|
| Go | 1.26.1+ | Build backend binary |
| Node.js | 20+ (CI: 22) | Build & run frontend |
| pnpm | 10.28+ | Package manager frontend |
| PostgreSQL | 17 + pgvector | Database |
| Git | 2.x | Clone repository |
| curl | any | Cek health endpoint |
| make | any | Menjalankan perintah build |

### Port yang Digunakan

| Port | Fungsi |
|------|--------|
| `3000` | Frontend (Next.js) |
| `8080` | Backend (Go API + WebSocket) |
| `5432` | PostgreSQL (internal, tidak perlu dibuka ke luar) |

---

## 2. Install Prerequisites

### Langkah 2.1 — Install Go 1.26.1+

```bash
# Download Go (cek versi terbaru di https://go.dev/dl/)
cd /tmp
wget https://go.dev/dl/go1.26.1.linux-amd64.tar.gz

# Hapus Go lama jika ada
sudo rm -rf /usr/local/go

# Extract
sudo tar -C /usr/local -xzf go1.26.1.linux-amd64.tar.gz

# Tambah ke PATH
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
echo 'export GOPATH=$HOME/go' >> ~/.bashrc
echo 'export PATH=$PATH:$GOPATH/bin' >> ~/.bashrc
source ~/.bashrc

# Verifikasi
go version
# Output: go version go1.26.1 linux/amd64
```

### Langkah 2.2 — Install Node.js 22+ dan pnpm

```bash
# Install Node.js via NodeSource
curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
sudo apt install -y nodejs

# Verifikasi Node.js
node --version
# Output: v22.x.x

# Install pnpm via corepack
corepack enable
corepack prepare pnpm@10.28.2 --activate

# Verifikasi pnpm
pnpm --version
# Output: 10.28.2
```

### Langkah 2.3 — Install make dan tools lainnya

```bash
sudo apt update
sudo apt install -y make git curl build-essential
```

### Langkah 2.4 — Verifikasi Semua Prerequisites

```bash
go version          # go1.26.1+
node --version      # v22+
pnpm --version      # 10.28+
make --version      # GNU Make 4.x
git --version       # 2.x
```

---

## 3. Clone Project

```bash
# Masuk ke direktori kerja
cd /opt

# Clone branch main
sudo git clone -b main https://github.com/multica-ai/multica.git multica

# Beri ownership ke user kamu
sudo chown -R $USER:$USER /opt/multica

# Masuk ke direktori project
cd /opt/multica

# Verifikasi file penting
ls -la
# Harus ada: Makefile, .env.example, server/, apps/, packages/
```

---

## 4. Setup Database PostgreSQL

Ada 2 opsi: install PostgreSQL natively atau gunakan Docker hanya untuk database.

### Opsi A: PostgreSQL via Docker (Lebih Mudah — Direkomendasikan)

Jika Docker sudah terinstall, ini cara termudah:

```bash
# Pastikan Docker terinstall
docker --version

# Mulai PostgreSQL menggunakan docker-compose.yml yang sudah ada
docker compose up -d postgres

# Verifikasi PostgreSQL berjalan
docker compose ps
# Harus ada: postgres  Up

# Tunggu hingga healthy
docker compose exec postgres pg_isready -U multica
# Output: /var/run/postgresql:5432 - accepting connections
```

PostgreSQL berjalan di `localhost:5432` dengan:
- User: `multica`
- Password: `multica`
- Database: `multica` (akan dibuat di Langkah 8)

> Keuntungan: tidak perlu install PostgreSQL natively, pgvector sudah include
> di image `pgvector/pgvector:pg17`.

### Opsi B: Install PostgreSQL 17 Natively (Tanpa Docker)

```bash
# Tambah PostgreSQL repository
sudo sh -c 'echo "deb https://apt.postgresql.org/pub/repos/apt $(lsb_release -cs)-pgdg main" > /etc/apt/sources.list.d/pgdg.list'
curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc | sudo gpg --dearmor -o /etc/apt/trusted.gpg.d/postgresql.gpg

# Install PostgreSQL 17
sudo apt update
sudo apt install -y postgresql-17 postgresql-17-pgvector

# Start PostgreSQL
sudo systemctl enable postgresql
sudo systemctl start postgresql

# Buat user dan database untuk Multica
sudo -u postgres psql << 'EOF'
CREATE USER multica WITH PASSWORD 'GantiPasswordKuatKamu123!';
CREATE DATABASE multica OWNER multica;
\c multica
CREATE EXTENSION IF NOT EXISTS vector;
GRANT ALL PRIVILEGES ON DATABASE multica TO multica;
EOF

# Verifikasi
psql -U multica -d multica -h localhost -c "SELECT extname FROM pg_extension;"
# Harus ada: vector
```

> Jika menggunakan password custom, update `DATABASE_URL` dan `POSTGRES_PASSWORD`
> di `.env` (lihat Langkah 5).

---

## 5. Konfigurasi Environment

### Langkah 5.1 — Salin Template `.env`

```bash
cd /opt/multica
cp .env.example .env
chmod 600 .env
```

### Langkah 5.2 — Generate Secrets

```bash
# Generate JWT_SECRET
JWT=$(openssl rand -hex 32)
sed -i "s/^JWT_SECRET=.*/JWT_SECRET=$JWT/" .env

# Generate POSTGRES_PASSWORD (skip jika install native dengan password custom)
PGPASS=$(openssl rand -hex 24)
sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$PGPASS/" .env
sed -i -E "s#^(DATABASE_URL=postgres://[^:]+:)[^@]*(@.*)#\1$PGPASS\2#" .env

echo "✓ Secret diisi otomatis"
```

> Jika menggunakan Opsi B (PostgreSQL native), pastikan `POSTGRES_PASSWORD` dan
> password di `DATABASE_URL` cocok dengan password yang kamu set saat membuat user.

### Langkah 5.3 — Edit `.env`

```bash
nano .env
```

**Konfigurasi wajib:**

```env
# Database — pastikan cocok dengan setup PostgreSQL kamu
POSTGRES_DB=multica
POSTGRES_USER=multica
POSTGRES_PASSWORD=<password kamu>
POSTGRES_PORT=5432
DATABASE_URL=postgres://multica:<password>@localhost:5432/multica?sslmode=disable

# Server
JWT_SECRET=<sudah diisi otomatis>
PORT=8080
FRONTEND_PORT=3000
FRONTEND_ORIGIN=http://localhost:3000

# Email (pilih salah satu untuk login)
# Opsi A: Resend
RESEND_API_KEY=re_xxxxxxxxxxxx
RESEND_FROM_EMAIL=noreply@domainkamu.com

# Opsi B: SMTP
# SMTP_HOST=smtp.domainkamu.com
# SMTP_PORT=587
# SMTP_USERNAME=user@domainkamu.com
# SMTP_PASSWORD=password
# SMTP_TLS=starttls

# Kontrol signup (opsional)
ALLOW_SIGNUP=true
# ALLOWED_EMAIL_DOMAINS=company.com
# DISABLE_WORKSPACE_CREATION=true
```

> ⚠️ Jika tidak ada email dikonfigurasi, kode verifikasi dicetak di log backend.
> Untuk testing lokal: set `APP_ENV=development` dan `MULTICA_DEV_VERIFICATION_CODE=888888`.

Simpan: `Ctrl+X` → `Y` → `Enter`

### Langkah 5.4 — Verifikasi

```bash
cat .env | grep -E "JWT_SECRET|POSTGRES_PASSWORD|DATABASE_URL|RESEND|SMTP_HOST"
```

Pastikan tidak ada nilai kosong (kecuali memang opsional).

---

## 6. Build Backend dari Source

### Langkah 6.1 — Install Go Dependencies

```bash
cd /opt/multica/server
go mod download
```

### Langkah 6.2 — Build Binary

```bash
cd /opt/multica

# Build semua binary (server, CLI, migrate)
make build
```

Atau build manual tanpa `make`:

```bash
cd /opt/multica/server

# Build backend server
go build -ldflags "-s -w" -o bin/server ./cmd/server

# Build CLI
go build -ldflags "-s -w" -o bin/multica ./cmd/multica

# Build migration tool
go build -ldflags "-s -w" -o bin/migrate ./cmd/migrate
```

### Langkah 6.3 — Verifikasi Binary

```bash
ls -la /opt/multica/server/bin/
# Harus ada: server, multica, migrate

# Test binary
/opt/multica/server/bin/server --help
# Atau
cd /opt/multica/server && ./bin/server --help
```

Output binary:

| Binary | Fungsi |
|--------|--------|
| `server/bin/server` | Backend API + WebSocket server |
| `server/bin/multica` | CLI untuk daemon dan workspace management |
| `server/bin/migrate` | Database migration tool |

---

## 7. Build Frontend dari Source

### Langkah 7.1 — Install Node.js Dependencies

```bash
cd /opt/multica

# Install semua dependencies (monorepo: apps/ + packages/)
pnpm install
```

> Proses ini mungkin memakan waktu beberapa menit pertama kali.

### Langkah 7.2 — Build Frontend (Production)

```bash
cd /opt/multica

# Build semua package (core, ui, views) dan web app
pnpm build
```

Atau build hanya web app:

```bash
cd /opt/multica
pnpm --filter @multica/web build
```

### Langkah 7.3 — Verifikasi Build

```bash
# Cek output build
ls -la /opt/multica/apps/web/.next/
# Harus ada folder: standalone/, static/, dll.
```

> Build menghasilkan Next.js standalone output yang siap dijalankan dengan Node.js.

---

## 8. Jalankan Database Migration

Migration harus dijalankan sebelum backend pertama kali start.

```bash
cd /opt/multica

# Load environment variables
export $(grep -v '^#' .env | xargs)

# Jalankan migration
./server/bin/migrate up
```

Output yang diharapkan:
```
Running migrations...
Migration 001_xxx applied
Migration 002_xxx applied
...
All migrations applied successfully
```

> Migration idempotent — menjalankan berkali-kali tidak ada efek samping.

---

## 9. Jalankan Multica

### Langkah 9.1 — Jalankan Backend

```bash
cd /opt/multica

# Load environment variables
export $(grep -v '^#' .env | xargs)

# Jalankan backend
./server/bin/server
```

Output yang diharapkan:
```
Starting server on :8080
Database connected
WebSocket handler registered
```

Backend berjalan di `http://localhost:8080`.

**Cek health:**
```bash
curl http://localhost:8080/health
# {"status":"ok"}

curl http://localhost:8080/readyz
# {"status":"ok","checks":{"db":"ok","migrations":"ok"}}
```

### Langkah 9.2 — Jalankan Frontend (Terminal Baru)

Buka terminal baru:

```bash
cd /opt/multica/apps/web

# Set API URL ke backend
export REMOTE_API_URL=http://localhost:8080

# Jalankan frontend (production mode)
pnpm start
```

Atau jika ingin specify port:

```bash
PORT=3000 REMOTE_API_URL=http://localhost:8080 pnpm start
```

Frontend berjalan di `http://localhost:3000`.

### Langkah 9.3 — Verifikasi

Buka browser: `http://localhost:3000`

Halaman login Multica harus tampil.

---

## 10. Setup systemd Service (Production)

Agar backend dan frontend berjalan otomatis dan restart saat crash.

### Langkah 10.1 — Backend Service

```bash
sudo nano /etc/systemd/system/multica-backend.service
```

Isi:

```ini
[Unit]
Description=Multica Backend (Go API + WebSocket)
After=network.target postgresql.service
# Jika menggunakan Docker untuk PostgreSQL, ganti dengan:
# After=network.target docker.service

[Service]
Type=simple
User=ubuntu
WorkingDirectory=/opt/multica
EnvironmentFile=/opt/multica/.env
ExecStart=/opt/multica/server/bin/server
Restart=always
RestartSec=5

# Security
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/opt/multica/data

[Install]
WantedBy=multi-user.target
```

> Ganti `User=ubuntu` dengan username kamu.
> Jika menggunakan Docker untuk PostgreSQL, ganti `After=` ke `docker.service`.

### Langkah 10.2 — Frontend Service

```bash
sudo nano /etc/systemd/system/multica-frontend.service
```

Isi:

```ini
[Unit]
Description=Multica Frontend (Next.js)
After=network.target multica-backend.service

[Service]
Type=simple
User=ubuntu
WorkingDirectory=/opt/multica/apps/web
Environment=REMOTE_API_URL=http://localhost:8080
Environment=NODE_ENV=production
Environment=PORT=3000
ExecStart=/usr/bin/node server.js
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

> Pastikan path `node` benar: `which node` → gunakan path lengkap.

### Langkah 10.3 — Aktifkan Services

```bash
# Reload systemd
sudo systemctl daemon-reload

# Enable (autostart saat boot)
sudo systemctl enable multica-backend
sudo systemctl enable multica-frontend

# Start
sudo systemctl start multica-backend
sudo systemctl start multica-frontend

# Cek status
sudo systemctl status multica-backend
sudo systemctl status multica-frontend
```

### Langkah 10.4 — Verifikasi

```bash
# Backend
curl http://localhost:8080/health
# {"status":"ok"}

# Frontend
curl -s http://localhost:3000 | head -5
# Harus ada HTML output

# Log backend
sudo journalctl -u multica-backend -f

# Log frontend
sudo journalctl -u multica-frontend -f
```

---

## 11. Install CLI & Start Daemon

Daemon berjalan di **mesin user** untuk mengeksekusi tugas AI agent.

### Langkah 11.1 — Install CLI

#### Opsi A: Build dari source (sudah ada dari Langkah 6)

```bash
# Binary sudah ada di /opt/multica/server/bin/multica
# Copy ke PATH
sudo cp /opt/multica/server/bin/multica /usr/local/bin/multica
```

#### Opsi B: Install via Homebrew (macOS/Linux)

```bash
brew install multica-ai/tap/multica
```

#### Opsi C: Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/multica-ai/multica/main/scripts/install.ps1 | iex
```

### Langkah 11.2 — Verifikasi CLI

```bash
multica version
# Output: multica v0.x.x
```

### Langkah 11.3 — Install AI Agent CLI (minimal 1)

Install minimal satu AI agent CLI di mesin kamu:

| Agent | Command | Install |
|-------|---------|---------|
| Claude Code | `claude` | [docs.anthropic.com](https://docs.anthropic.com/en/docs/claude-code) |
| Codex | `codex` | [github.com/openai/codex](https://github.com/openai/codex) |
| Copilot | `copilot` | [docs.github.com/en/copilot](https://docs.github.com/en/copilot) |
| OpenCode | `opencode` | [github.com/anomalyco/opencode](https://github.com/anomalyco/opencode) |

### Langkah 11.4 — Konfigurasi & Start Daemon

```bash
# One-command setup untuk self-hosted
multica setup self-host
```

Atau manual:

```bash
# Set URL server
multica config set server_url http://localhost:8080
multica config set app_url http://localhost:3000

# Login (buka browser)
multica login

# Start daemon
multica daemon start
```

### Langkah 11.5 — Verifikasi Daemon

```bash
multica daemon status
```

Pastikan:
1. Status: `running`
2. Minimal 1 agent terdeteksi
3. Minimal 1 workspace ter-watch

---

## 12. Login & First-Time Setup

### Login

1. Buka `http://localhost:3000` di browser
2. Masukkan email → klik **Send Code**
3. Cek email untuk kode verifikasi 6 digit
4. Masukkan kode → login

> Jika email belum dikonfigurasi, lihat kode di log:
> ```bash
> sudo journalctl -u multica-backend | grep "Verification code"
> ```

### Setup

1. **Settings → Runtimes** — verifikasi mesin kamu terdaftar
2. **Settings → Agents** — buat agent pertama
3. Buat issue → assign ke agent → agent mengeksekusi tugas

---

## 13. Workflow Customisasi UI

Multica frontend adalah monorepo dengan struktur:

```
packages/
├── ui/          ← Komponen atomic (button, card, dialog, dll.)
├── views/       ← Halaman & komponen business (issue board, settings, dll.)
├── core/        ← Headless logic (stores, hooks, API client)
└── tsconfig/    ← Shared TypeScript config

apps/
├── web/         ← Next.js App Router (platform wiring, routing)
├── desktop/     ← Electron app
└── mobile/      ← Expo / React Native
```

### Apa yang Bisa Diubah?

| Yang Ingin Diubah | Lokasi | Contoh |
|-------------------|--------|--------|
| Warna / tema | `packages/ui/styles/` | CSS variables, design tokens |
| Komponen UI | `packages/ui/` | Button, Card, Dialog, dll. |
| Halaman business | `packages/views/` | Issue board, settings, profile |
| Routing / layout | `apps/web/app/` | Halaman, layout, middleware |
| Platform wiring | `apps/web/platform/` | Next.js APIs, cookies, redirects |
| Teks / i18n | `packages/views/locales/` | Terjemahan, glossary |

### Aturan Penting (dari CLAUDE.md)

- `packages/ui/` — **zero** `@multica/core` imports, **zero** business logic
- `packages/views/` — **no** `next/*`, **no** `react-router-dom`, use `NavigationAdapter`
- `packages/core/` — **zero** `react-dom`, **zero** `localStorage`, **zero** `process.env`
- `apps/web/platform/` — **only place** for Next.js APIs

### Contoh Customisasi: Ubah Warna Tema

```bash
# Lihat file CSS tokens
cat packages/ui/styles/globals.css
```

Edit semantic tokens (contoh):

```css
:root {
  --background: oklch(0.145 0 0);        /* ubah warna background */
  --foreground: oklch(0.985 0 0);        /* ubah warna text */
  --primary: oklch(0.205 0 0);           /* ubah warna primary */
  --primary-foreground: oklch(0.985 0 0);
  /* ... token lainnya */
}
```

### Contoh Customisasi: Ubah Komponen

```bash
# Cari komponen yang ingin diubah
# Contoh: cari komponen Button
find packages/ui -name "*.tsx" | grep -i button
```

Edit file, lalu rebuild:

```bash
cd /opt/multica
pnpm build
```

Restart frontend:

```bash
sudo systemctl restart multica-frontend
```

### Rebuild Setelah Perubahan UI

```bash
# 1. Edit file di packages/ui/, packages/views/, atau apps/web/

# 2. Rebuild frontend
cd /opt/multica
pnpm build

# 3. Restart frontend service
sudo systemctl restart multica-frontend

# Atau jika jalankan manual (bukan systemd):
# Stop frontend (Ctrl+C), lalu jalankan ulang:
cd /opt/multica/apps/web
REMOTE_API_URL=http://localhost:8080 pnpm start
```

---

## 14. Workflow Customisasi Backend

Backend Go ada di `server/`:

```
server/
├── cmd/
│   ├── server/          ← Entry point backend server
│   ├── multica/         ← Entry point CLI
│   ├── migrate/         ← Entry point migration tool
│   └── backfill_*/      ← Utility tools
├── internal/
│   ├── handler/         ← HTTP handlers (API endpoints)
│   ├── service/         ← Business logic
│   ├── store/           ← Database access (sqlc generated)
│   ├── auth/            ← Authentication
│   └── ...
├── migrations/          ← SQL migration files
└── go.mod
```

### Apa yang Bisa Diubah?

| Yang Ingin Diubah | Lokasi |
|-------------------|--------|
| API endpoint baru | `server/internal/handler/` |
| Logika business | `server/internal/service/` |
| Query database | `server/migrations/` + `sqlc generate` |
| Autentikasi | `server/internal/auth/` |
| WebSocket handler | `server/internal/handler/` (cari `ws` atau `websocket`) |

### Contoh Customisasi: Tambah API Endpoint

1. Tulis SQL query di `server/migrations/` (jika butuh tabel/kolom baru)
2. Jalankan migration: `./server/bin/migrate up`
3. Generate sqlc code: `cd server && sqlc generate`
4. Tambah handler di `server/internal/handler/`
5. Daftarkan route di router (cari file yang setup Chi routes)

### Rebuild Setelah Perubahan Backend

```bash
# 1. Edit file di server/

# 2. Rebuild binary
cd /opt/multica
make build

# 3. Restart backend service
sudo systemctl restart multica-backend

# Atau jika jalankan manual:
# Stop backend (Ctrl+C), lalu:
cd /opt/multica
export $(grep -v '^#' .env | xargs)
./server/bin/server
```

### Rebuild CLI Setelah Perubahan

Jika kamu juga mengubah code CLI (`server/cmd/multica/`):

```bash
cd /opt/multica
make build

# Update CLI di PATH
sudo cp server/bin/multica /usr/local/bin/multica

# Restart daemon
multica daemon stop
multica daemon start
```

---

## 15. Development Mode (Hot Reload)

Untuk development, gunakan mode hot reload — tidak perlu rebuild setiap perubahan.

### Langkah 15.1 — Start Backend (Terminal 1)

```bash
cd /opt/multica

# Load env
export $(grep -v '^#' .env | xargs)

# Jalankan backend dengan go run (auto-recompile saat file berubah)
cd server
go run ./cmd/server
```

> `go run` otomatis recompile saat ada perubahan file Go. Tapi tidak hot reload
> seperti HMR — kamu perlu stop (Ctrl+C) dan jalankan ulang untuk melihat perubahan.

### Langkah 15.2 — Start Frontend (Terminal 2)

```bash
cd /opt/multica

# Jalankan frontend dalam dev mode (HMR — hot module replacement)
pnpm dev:web
```

> Next.js dev mode memiliki **Hot Module Replacement** — perubahan UI langsung
> terlihat di browser tanpa refresh. Sangat cepat untuk iterasi customisasi UI.

### Langkah 15.3 — Akses Dev Mode

Buka `http://localhost:3000` — frontend dalam dev mode dengan HMR aktif.

### Workflow Development Cepat

```
1. Edit packages/ui/ atau packages/views/  →  HMR auto-update di browser
2. Edit apps/web/                           →  HMR auto-update di browser
3. Edit server/                             →  Ctrl+C, go run ./cmd/server ulang
4. Edit database schema                     →  ./server/bin/migrate up
```

### One-Command Dev

Multica juga punya `make dev` yang melakukan semuanya sekaligus:

```bash
cd /opt/multica
make dev
```

Ini akan:
1. Cek prerequisites (Node.js, pnpm, Go, Docker)
2. Buat `.env` jika belum ada
3. Install dependencies
4. Start PostgreSQL (via Docker)
5. Run migrations
6. Start backend (`go run ./cmd/server`)
7. Start frontend (`pnpm dev:web`)

> `make dev` butuh Docker untuk PostgreSQL. Jika install PostgreSQL native,
> pastikan PostgreSQL sudah berjalan sebelum `make dev`.

---

## 16. Operasi Sehari-hari

### Start / Stop (systemd)

```bash
# Start
sudo systemctl start multica-backend
sudo systemctl start multica-frontend

# Stop
sudo systemctl stop multica-backend
sudo systemctl stop multica-frontend

# Restart
sudo systemctl restart multica-backend
sudo systemctl restart multica-frontend

# Status
sudo systemctl status multica-backend
sudo systemctl status multica-frontend
```

### Start / Stop (Manual)

```bash
# Backend
cd /opt/multica
export $(grep -v '^#' .env | xargs)
./server/bin/server

# Frontend (terminal baru)
cd /opt/multica/apps/web
REMOTE_API_URL=http://localhost:8080 pnpm start
```

### Lihat Log

```bash
# Backend (systemd)
sudo journalctl -u multica-backend -f

# Frontend (systemd)
sudo journalctl -u multica-frontend -f

# Backend (manual) — output langsung ke terminal
# Frontend (manual) — output langsung ke terminal
```

### Update ke Versi Terbaru

```bash
cd /opt/multica

# Pull kode terbaru
git pull origin main

# Rebuild backend
make build

# Rebuild frontend
pnpm install
pnpm build

# Run migration (jika ada migration baru)
./server/bin/migrate up

# Restart services
sudo systemctl restart multica-backend
sudo systemctl restart multica-frontend

# Update CLI
sudo cp server/bin/multica /usr/local/bin/multica
multica daemon stop && multica daemon start
```

### Database Migration

```bash
# Apply migration terbaru
cd /opt/multica
export $(grep -v '^#' .env | xargs)
./server/bin/migrate up

# Rollback migration terakhir
./server/bin/migrate down
```

### Regenerate sqlc (setelah ubah SQL query)

```bash
cd /opt/multica/server
sqlc generate
```

> Install sqlc: `go install github.com/kyleconroy/sqlc/cmd/sqlc@latest`

### Operasi CLI & Daemon

```bash
multica daemon status          # cek status daemon
multica daemon logs -f         # log daemon real-time
multica daemon stop            # stop daemon
multica daemon start           # start daemon
multica workspace list         # list workspace
multica issue list             # list issue
multica issue create --title "Fix bug" --assignee "AgentName"
```

---

## 17. Troubleshooting

### Backend gagal start: "connection refused" ke database

```bash
# 1. Cek PostgreSQL berjalan
# Jika Docker:
docker compose ps
docker compose up -d postgres

# Jika native:
sudo systemctl status postgresql
sudo systemctl start postgresql

# 2. Cek DATABASE_URL di .env
cat .env | grep DATABASE_URL
# Pastikan host, port, user, password benar

# 3. Test koneksi
psql -U multica -d multica -h localhost -c "SELECT 1;"
# Jika Docker:
docker compose exec postgres psql -U multica -d multica -c "SELECT 1;"
```

### Backend gagal start: "JWT_SECRET is change-me-in-production"

```bash
# Generate JWT_SECRET baru
JWT=$(openssl rand -hex 32)
sed -i "s/^JWT_SECRET=.*/JWT_SECRET=$JWT/" .env

# Restart
sudo systemctl restart multica-backend
```

### Frontend gagal start: "Cannot find module"

```bash
# Reinstall dependencies
cd /opt/multica
pnpm install

# Rebuild
pnpm build

# Restart
sudo systemctl restart multica-frontend
```

### Frontend tidak bisa connect ke backend (API error)

```bash
# 1. Cek backend berjalan
curl http://localhost:8080/health

# 2. Cek REMOTE_API_URL di frontend service
sudo systemctl show multica-frontend | grep REMOTE_API_URL
# Harus: http://localhost:8080

# 3. Jika jalankan manual, pastikan env var set:
REMOTE_API_URL=http://localhost:8080 pnpm start
```

### WebSocket tidak berjalan (real-time tidak update)

```bash
# Cek dari browser:
# 1. Buka DevTools (F12) → Network → WS
# 2. Harus ada koneksi ke /ws dengan status 101

# Jika gagal:
# - Pastikan backend berjalan di 8080
# - Pastikan CORS_ALLOWED_ORIGINS di .env include origin frontend
# - Jika akses dari LAN, set FRONTEND_ORIGIN dan CORS_ALLOWED_ORIGINS
```

### Build gagal: "go: command not found"

```bash
# Pastikan Go di PATH
export PATH=$PATH:/usr/local/go/bin
source ~/.bashrc

# Verifikasi
go version
```

### Build gagal: "pnpm: command not found"

```bash
# Enable corepack
corepack enable
corepack prepare pnpm@10.28.2 --activate

# Verifikasi
pnpm --version
```

### Build gagal: "sqlc: command not found"

```bash
# Install sqlc
go install github.com/kyleconroy/sqlc/cmd/sqlc@latest

# Pastikan GOPATH/bin di PATH
export PATH=$PATH:$HOME/go/bin
```

### Login gagal: tidak menerima kode email

```bash
# Cek konfigurasi email
cat .env | grep -E "RESEND|SMTP"

# Jika belum dikonfigurasi, lihat kode di log:
sudo journalctl -u multica-backend | grep "Verification code"

# Untuk testing lokal, gunakan kode tetap:
# Edit .env: APP_ENV=development, MULTICA_DEV_VERIFICATION_CODE=888888
# Restart backend
```

### Daemon tidak mendeteksi agent

```bash
# Pastikan AI CLI terinstall
which claude || which codex || which copilot

# Restart daemon
multica daemon stop
multica daemon start

# Cek log
multica daemon logs
```

### Port sudah digunakan

```bash
# Cek apa yang menggunakan port
sudo lsof -i :8080
sudo lsof -i :3000

# Kill proses lama
sudo kill -9 $(lsof -ti :8080)

# Atau ubah port di .env:
# PORT=9090
# FRONTEND_PORT=4000
```

### Reset Database

```bash
# ⚠️ Hapus semua data!
cd /opt/multica
export $(grep -v '^#' .env | xargs)

# Jika Docker:
docker compose exec postgres psql -U multica -d postgres \
  -c "DROP DATABASE IF EXISTS multica WITH (FORCE);" \
  -c "CREATE DATABASE multica;"

# Jika native:
sudo -u postgres psql -c "DROP DATABASE IF EXISTS multica WITH (FORCE);"
sudo -u postgres psql -c "CREATE DATABASE multica OWNER multica;"
sudo -u postgres psql -d multica -c "CREATE EXTENSION IF NOT EXISTS vector;"

# Run migration ulang
./server/bin/migrate up
```

---

## Ringkasan Alur Instalasi (Checklist)

### Prerequisites & Database

```
[ ] 1. Install Go 1.26.1+, Node.js 22+, pnpm 10.28+, make, git
[ ] 2. Setup PostgreSQL 17 + pgvector (Docker atau native)
[ ] 3. git clone -b main https://github.com/multica-ai/multica.git multica
[ ] 4. cd multica
[ ] 5. cp .env.example .env && chmod 600 .env
[ ] 6. Generate JWT_SECRET dan POSTGRES_PASSWORD
[ ] 7. nano .env → isi email config (RESEND_API_KEY atau SMTP_HOST)
```

### Build & Run

```
[ ] 8.  make build              ← build backend binary (server, multica, migrate)
[ ] 9.  pnpm install            ← install frontend dependencies
[ ] 10. pnpm build              ← build frontend (Next.js production)
[ ] 11. ./server/bin/migrate up ← jalankan database migration
[ ] 12. ./server/bin/server     ← start backend (port 8080)
[ ] 13. cd apps/web && REMOTE_API_URL=http://localhost:8080 pnpm start  ← start frontend (port 3000)
[ ] 14. curl http://localhost:8080/health → {"status":"ok"}
```

### systemd (Production)

```
[ ] 15. Buat /etc/systemd/system/multica-backend.service
[ ] 16. Buat /etc/systemd/system/multica-frontend.service
[ ] 17. sudo systemctl enable multica-backend multica-frontend
[ ] 18. sudo systemctl start multica-backend multica-frontend
```

### CLI & Daemon

```
[ ] 19. sudo cp server/bin/multica /usr/local/bin/  ← install CLI
[ ] 20. Install AI agent CLI (claude, codex, copilot, dll.)
[ ] 21. multica setup self-host  ← konfigurasi + login + start daemon
[ ] 22. multica daemon status    ← verifikasi running + agent terdeteksi
```

### First-Time Setup

```
[ ] 23. Buka http://localhost:3000 → login dengan email
[ ] 24. Settings → Runtimes → verifikasi mesin terdaftar
[ ] 25. Settings → Agents → buat agent
[ ] 26. Buat issue → assign ke agent → done ✅
```

### Customisasi

```
[ ] 27. Edit packages/ui/ atau packages/views/ untuk UI
[ ] 28. pnpm build → sudo systemctl restart multica-frontend
[ ] 29. Edit server/ untuk backend
[ ] 30. make build → sudo systemctl restart multica-backend
[ ] 31. Atau gunakan make dev untuk hot reload development
```

---

## Perbandingan: Docker vs Bare Metal vs Development

```
DOCKER (make selfhost):
  3 container → PostgreSQL + Backend + Frontend
  ✅ Setup paling mudah, satu perintah
  ❌ Customisasi butuh rebuild Docker image
  ❌ Tidak ada hot reload

BARE METAL (panduan ini):
  PostgreSQL (Docker/native) + Go binary + Node.js process
  ✅ Customisasi mudah — edit, rebuild, restart
  ✅ Kontrol penuh konfigurasi
  ✅ Bisa setup systemd untuk production
  ❌ Setup lebih banyak langkah
  ❌ Frontend butuh Node.js runtime (bukan single binary)

DEVELOPMENT (make dev):
  PostgreSQL (Docker) + go run + pnpm dev:web (HMR)
  ✅ Hot reload frontend (HMR — langsung terlihat di browser)
  ✅ Backend auto-recompile (go run)
  ✅ One command: make dev
  ❌ Tidak untuk production
  ❌ Butuh Docker untuk PostgreSQL
```

---

## Referensi Dokumentasi Tambahan

| Dokumen | Isi |
|---------|-----|
| [installation.md](../installation.md) | Panduan instalasi binary (download pre-compiled) |
| [SELF_HOSTING.md](../SELF_HOSTING.md) | Panduan self-hosting via Docker |
| [SELF_HOSTING_ADVANCED.md](../SELF_HOSTING_ADVANCED.md) | Konfigurasi advanced (reverse proxy, S3, database) |
| [CLI_AND_DAEMON.md](../CLI_AND_DAEMON.md) | Referensi CLI & daemon commands |
| [CONTRIBUTING.md](../CONTRIBUTING.md) | Panduan development & kontribusi |
| [CLAUDE.md](../CLAUDE.md) | Aturan arsitektur, coding rules, package boundaries |
| [support_docs/installation.md](installation.md) | Panduan instalasi Docker (format GoClaw) |
