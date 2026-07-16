# Panduan Migrasi Multica — Server Lama ke Server Baru

> **Pindahkan instalasi Multica dari server lama ke server baru tanpa kehilangan
> data.** Panduan ini cocok untuk migrasi dari server dev ke server production,
> atau pindah hosting/VPS. Semua data (database, file upload, konfigurasi,
> daemon) dipindahkan dengan downtime minimal.

---

## Daftar Isi

1. [Prasyarat](#1-prasyarat)
2. [Apa yang Perlu Dimigrasi?](#2-apa-yang-perlu-dimigrasi)
3. [Persiapan di Server Baru](#3-persiapan-di-server-baru)
4. [Backup Data di Server Lama](#4-backup-data-di-server-lama)
5. [Transfer Data ke Server Baru](#5-transfer-data-ke-server-baru)
6. [Restore Data di Server Baru](#6-restore-data-di-server-baru)
7. [Verifikasi & Cutover](#7-verifikasi--cutover)
8. [Post-Migration: Update DNS & Client](#8-post-migration-update-dns--client)
9. [Rollback (jika gagal)](#9-rollback-jika-gagal)
10. [Troubleshooting Migrasi](#10-troubleshooting-migrasi)

---

## 1. Prasyarat

### Akses yang Dibutuhkan

| Akses | Server Lama | Server Baru |
|-------|-------------|------------|
| SSH | Ya | Ya |
| sudo / root | Ya | Ya |
| User `multica` | Ya (jika ada) | Ya (buat via panduan instalasi) |
| PostgreSQL | Ya | Ya (sudah terinstall) |

### Software di Server Baru

Server baru **harus sudah terinstall** sebelum migrasi:

- Go 1.26.1+
- Node.js 22+ dan pnpm 10.28.2
- PostgreSQL 17 + pgvector (Docker atau native)
- Git, make, openssl, rsync

> 📖 Ikuti panduan [installation-bare-metal-home.md](installation-bare-metal-home.md)
> **sampai Langkah 9** (database migration) di server baru sebelum migrasi.
> **Jangan jalankan migration** di server baru — kita akan restore dari backup
> server lama.

### Estimasi Waktu & Downtime

| Ukuran Data | Estimasi Backup | Estimasi Transfer | Estimasi Restore | Total Downtime |
|-------------|-----------------|-------------------|------------------|----------------|
| < 1 GB | 2-5 menit | 1-5 menit | 2-5 menit | ~15 menit |
| 1-10 GB | 5-20 menit | 5-30 menit | 10-30 menit | ~1 jam |
| > 10 GB | 20-60 menit | 30-120 menit | 30-90 menit | ~3 jam |

---

## 2. Apa yang Perlu Dimigrasi?

```
┌─────────────────────────────────────────────────────────────┐
│                    SERVER LAMA                               │
│                                                              │
│  /home/multica/multica/                                      │
│  ├── .env                          ───┐                      │
│  ├── data/uploads/                 ───┤  Backup & transfer   │
│  │   └── (file attachment)             │                      │
│  └── server/                       ───┘                      │
│                                                              │
│  PostgreSQL (multica database)    ───┐                       │
│  ├── issues, members, agents,        │  pg_dump → pg_restore │
│  │   workspaces, messages, dll.       │                      │
│  └── pgvector embeddings          ───┘                       │
│                                                              │
│  ~/.config/multica/ (daemon)      ───┐                       │
│  ├── config.toml                     │  rsync (opsional)     │
│  ├── auth.json                       │                      │
│  └── daemon-state.json           ───┘                       │
└─────────────────────────────────────────────────────────────┘
                          │
                          │  rsync / scp / pg_dump
                          ▼
┌─────────────────────────────────────────────────────────────┐
│                    SERVER BARU                               │
│                                                              │
│  /home/multica/multica/                                      │
│  ├── .env  (dari server lama, edit jika perlu)              │
│  ├── data/uploads/  (dari server lama)                      │
│  └── server/  (sudah ada dari build)                        │
│                                                              │
│  PostgreSQL (multica database)    ← restore dari dump        │
│                                                              │
│  ~/.config/multica/ (daemon)      ← rsync dari server lama  │
└─────────────────────────────────────────────────────────────┘
```

### Checklist Data yang Perlu Dimigrasi

| Data | Lokasi Server Lama | Metode | Wajib? |
|------|-------------------|--------|--------|
| Database | PostgreSQL `multica` | `pg_dump` → `pg_restore` | ✅ Ya |
| File upload (lokal) | `/home/multica/multica/data/uploads/` | `rsync` | ✅ Ya (jika pakai S3, skip) |
| Konfigurasi `.env` | `/home/multica/multica/.env` | `rsync` / copy | ✅ Ya |
| Source code | `/home/multica/multica/` (git repo) | `git clone` di server baru | ❌ Tidak (re-clone) |
| Binary build | `server/bin/*` | Rebuild di server baru | ❌ Tidak (rebuild) |
| Daemon config | `~/.config/multica/` | `rsync` | ⚠️ Opsional (bisa re-login) |
| CLI binary | `/usr/local/bin/multica` | Rebuild di server baru | ❌ Tidak (rebuild) |

> 💡 **Penting**: Source code dan binary **tidak perlu dimigrasi** — lebih aman
> re-clone dari git dan rebuild di server baru. Yang perlu dimigrasi hanya
> **data** (database, file upload) dan **konfigurasi** (`.env`).

---

## 3. Persiapan di Server Baru

### Langkah 3.1 — Install Multica (sampai build, sebelum migration)

Ikuti panduan [installation-bare-metal-home.md](installation-bare-metal-home.md):

```
[ ] 1. Buat user multica
[ ] 2. Install prerequisites (Go, Node.js, pnpm, PostgreSQL)
[ ] 3. Clone project ke /home/multica/multica
[ ] 4. Setup PostgreSQL (Docker atau native) — buat database kosong
[ ] 5. cp .env.example .env (jangan generate secret dulu)
[ ] 6. make build (build backend binary)
[ ] 7. pnpm install && pnpm build (build frontend)
```

> ⚠️ **STOP di sini.** Jangan jalankan `./server/bin/migrate up` di server baru.
> Database akan diisi dari backup server lama.

### Langkah 3.2 — Pastikan PostgreSQL Berjalan di Server Baru

```bash
# Jika Docker
cd /home/multica/multica
docker compose up -d postgres
docker compose ps
# Harus: postgres  Up

# Jika native
sudo systemctl status postgresql
sudo systemctl start postgresql
```

### Langkah 3.3 — Buat Database Kosong di Server Baru

```bash
# Jika Docker
docker compose exec postgres psql -U multica -d postgres \
  -c "DROP DATABASE IF EXISTS multica WITH (FORCE);" \
  -c "CREATE DATABASE multica;"

# Jika native
sudo -u postgres psql \
  -c "DROP DATABASE IF EXISTS multica WITH (FORCE);" \
  -c "CREATE DATABASE multica OWNER multica;"
sudo -u postgres psql -d multica -c "CREATE EXTENSION IF NOT EXISTS vector;"
```

> Database kosong akan diisi dari dump server lama.

### Langkah 3.4 — Catat IP/Hostname Server Baru

```bash
# Di server baru
hostname -I
# Output: 192.168.x.x  atau IP publik

# Atau cek IP publik
curl -s ifconfig.me
```

Catat IP ini — akan digunakan untuk transfer data dan update DNS.

---

## 4. Backup Data di Server Lama

### Langkah 4.1 — Stop Services (Mulai Downtime)

```bash
# Di server lama
sudo systemctl stop multica-frontend
sudo systemctl stop multica-backend

# Atau jika jalankan manual, stop dengan Ctrl+C di setiap terminal

# Verifikasi services berhenti
sudo systemctl status multica-backend
sudo systemctl status multica-frontend
# Harus: inactive (dead)
```

> ⚠️ **Downtime dimulai di sini.** User tidak bisa akses Multica sampai server
> baru selesai dikonfigurasi.

### Langkah 4.2 — Backup Database PostgreSQL

```bash
# Di server lama, sebagai user multica
sudo su - multica
cd /home/multica/multica

# Load env untuk dapat DATABASE_URL
export $(grep -v '^#' .env | xargs)

# Backup database (format custom untuk restore cepat + pgvector support)
pg_dump \
  --format=custom \
  --no-owner \
  --no-privileges \
  --file=/tmp/multica-db-backup.dump \
  "$(grep '^DATABASE_URL=' .env | cut -d= -f2-)"

# Verifikasi backup
ls -lh /tmp/multica-db-backup.dump
# Output: -rw-r--r-- 1 multica multica 123M ... /tmp/multica-db-backup.dump

# Test integritas backup (list isi tanpa restore)
pg_restore --list /tmp/multica-db-backup.dump | head -20
# Harus ada list tabel: issues, members, agents, workspaces, dll.
```

> **Opsi alternatif** jika `pg_dump` dari client tidak punya akses langsung:
> ```bash
> # Jika Docker
> docker compose exec -T postgres pg_dump \
>   --format=custom \
>   --no-owner \
>   --no-privileges \
>   -U multica -d multica > /tmp/multica-db-backup.dump
>
> # Jika native
> sudo -u postgres pg_dump \
>   --format=custom \
>   --no-owner \
>   --no-privileges \
>   -d multica > /tmp/multica-db-backup.dump
> ```

### Langkah 4.3 — Backup File Upload (jika pakai local storage)

```bash
# Di server lama, sebagai user multica
# Cek apakah pakai local storage atau S3
grep S3_BUCKET /home/multica/multica/.env
# Jika S3_BUCKET kosong → pakai local storage, perlu backup
# Jika S3_BUCKET terisi → file di S3, skip langkah ini

# Cek ukuran folder upload
du -sh /home/multica/multica/data/uploads/
# Output: 1.2G    /home/multica/multica/data/uploads/

# Buat tarball untuk transfer cepat
cd /home/multica/multica
tar -czf /tmp/multica-uploads.tar.gz -C data uploads

# Verifikasi
ls -lh /tmp/multica-uploads.tar.gz
```

### Langkah 4.4 — Backup Konfigurasi `.env`

```bash
# Di server lama
cp /home/multica/multica/.env /tmp/multica-env-backup

# Verifikasi
cat /tmp/multica-env-backup | grep -E "JWT_SECRET|DATABASE_URL|APP_ENV"
```

> ⚠️ File `.env` berisi secret sensitif (JWT_SECRET, password DB, API key).
> Transfer via SSH (encrypted) dan hapus dari `/tmp` setelah selesai.

### Langkah 4.5 — Backup Daemon Config (Opsional)

```bash
# Di server lama, sebagai user multica
# Cek apakah daemon pernah di-setup
ls -la ~/.config/multica/ 2>/dev/null

# Jika ada, backup
if [ -d ~/.config/multica ]; then
  tar -czf /tmp/multica-daemon-config.tar.gz -C ~ .config/multica
  ls -lh /tmp/multica-daemon-config.tar.gz
fi
```

> Daemon config berisi auth token dan server URL. Jika server URL berubah,
> lebih baik re-login di server baru (lihat Langkah 8.3).

### Langkah 4.6 — Verifikasi Semua Backup

```bash
# Di server lama
echo "=== Backup Files ==="
ls -lh /tmp/multica-db-backup.dump
ls -lh /tmp/multica-uploads.tar.gz 2>/dev/null || echo "(S3 mode — no upload backup needed)"
ls -lh /tmp/multica-env-backup
ls -lh /tmp/multica-daemon-config.tar.gz 2>/dev/null || echo "(no daemon config)"

echo ""
echo "=== Database Backup Integrity ==="
pg_restore --list /tmp/multica-db-backup.dump | grep -c "^;" 
# Output: jumlah object di dump (harus > 0)
```

---

## 5. Transfer Data ke Server Baru

### Langkah 5.1 — Dari Server Lama, Transfer ke Server Baru

```bash
# Di server lama
# Ganti NEW_SERVER_IP dengan IP server baru
NEW_SERVER_IP=192.168.x.x

# Transfer database backup
scp /tmp/multica-db-backup.dump multica@$NEW_SERVER_IP:/tmp/

# Transfer upload backup (jika pakai local storage)
scp /tmp/multica-uploads.tar.gz multica@$NEW_SERVER_IP:/tmp/

# Transfer .env backup
scp /tmp/multica-env-backup multica@$NEW_SERVER_IP:/tmp/

# Transfer daemon config (opsional)
scp /tmp/multica-daemon-config.tar.gz multica@$NEW_SERVER_IP:/tmp/ 2>/dev/null
```

> 💡 **Tips**: Jika server lama tidak punya akses SSH ke server baru, lakukan
> sebaliknya — dari server baru, `scp` dari server lama:
> ```bash
> # Di server baru
> OLD_SERVER_IP=192.168.y.y
> scp multica@$OLD_SERVER_IP:/tmp/multica-db-backup.dump /tmp/
> scp multica@$OLD_SERVER_IP:/tmp/multica-uploads.tar.gz /tmp/
> scp multica@$OLD_SERVER_IP:/tmp/multica-env-backup /tmp/
> ```

### Langkah 5.2 — Alternatif: rsync untuk File Besar

Jika file upload besar (> 1 GB), gunakan `rsync` untuk transfer yang bisa resume:

```bash
# Di server lama
NEW_SERVER_IP=192.168.x.x

# Transfer upload folder langsung (tanpa tarball, bisa resume)
rsync -avz --progress \
  /home/multica/multica/data/uploads/ \
  multica@$NEW_SERVER_IP:/home/multica/multica/data/uploads/
```

### Langkah 5.3 — Verifikasi Transfer di Server Baru

```bash
# Di server baru
ls -lh /tmp/multica-db-backup.dump
ls -lh /tmp/multica-uploads.tar.gz 2>/dev/null
ls -lh /tmp/multica-env-backup
```

Pastikan ukuran file sama dengan di server lama.

---

## 6. Restore Data di Server Baru

### Langkah 6.1 — Restore Konfigurasi `.env`

```bash
# Di server baru, sebagai user multica
sudo su - multica
cd /home/multica/multica

# Backup .env lama (dari install) untuk referensi
cp .env .env.install-template

# Restore .env dari server lama
cp /tmp/multica-env-backup .env
chmod 600 .env
```

### Langkah 6.2 — Edit `.env` untuk Server Baru

Beberapa nilai di `.env` perlu disesuaikan dengan server baru:

```bash
nano .env
```

**Yang perlu diubah:**

```env
# Database — pastikan cocok dengan PostgreSQL di server baru
# Jika password DB berbeda, update di sini
POSTGRES_PASSWORD=<password DB server baru>
DATABASE_URL=postgres://multica:<password>@localhost:5432/multica?sslmode=disable

# Frontend origin — update jika domain/IP berubah
FRONTEND_ORIGIN=http://<IP-ATAU-DOMAIN-SERVER-BARU>:3000
MULTICA_APP_URL=http://<IP-ATAU-DOMAIN-SERVER-BARU>:3000

# CORS — tambahkan origin baru jika akses dari domain berbeda
CORS_ALLOWED_ORIGINS=http://<IP-ATAU-DOMAIN-SERVER-BARU>:3000

# JWT_SECRET — PERTAHANKAN dari server lama (jangan generate baru!)
# Ini penting agar session user yang sudah login tetap valid
JWT_SECRET=<dari server lama, jangan diubah>

# Google OAuth — update redirect URI jika domain berubah
GOOGLE_REDIRECT_URI=http://<IP-ATAU-DOMAIN-SERVER-BARU>:3000/auth/callback
```

> ⚠️ **KRITIS**: Jangan generate `JWT_SECRET` baru! Pertahankan nilai dari server
> lama agar session user yang sudah login tetap valid. Jika diganti, semua user
> harus login ulang.

Simpan: `Ctrl+X` → `Y` → `Enter`

### Langkah 6.3 — Restore Database

```bash
# Di server baru, sebagai user multica
sudo su - multica
cd /home/multica/multica

# Load env
export $(grep -v '^#' .env | xargs)

# Restore database dari dump
pg_restore \
  --dbname="$(grep '^DATABASE_URL=' .env | cut -d= -f2-)" \
  --no-owner \
  --no-privileges \
  --clean \
  --if-exists \
  /tmp/multica-db-backup.dump
```

> **Opsi alternatif** jika `pg_restore` dari client tidak punya akses langsung:
> ```bash
> # Jika Docker
> docker compose exec -T postgres pg_restore \
>   --dbname=multica \
>   --username=multica \
>   --no-owner \
>   --no-privileges \
>   --clean \
>   --if-exists \
#   < /tmp/multica-db-backup.dump
>
> # Jika native
> sudo -u postgres pg_restore \
>   --dbname=multica \
>   --no-owner \
>   --no-privileges \
>   --clean \
>   --if-exists \
>   /tmp/multica-db-backup.dump
> ```

### Langkah 6.4 — Verifikasi Database Restore

```bash
# Di server baru
# Cek jumlah tabel
psql -U multica -d multica -h localhost -c "\dt" | head -30
# Atau jika Docker:
# docker compose exec postgres psql -U multica -d multica -c "\dt"

# Cek data di tabel penting
psql -U multica -d multica -h localhost << 'EOF'
SELECT 'workspaces' as table, count(*) FROM workspaces
UNION ALL
SELECT 'members', count(*) FROM members
UNION ALL
SELECT 'issues', count(*) FROM issues
UNION ALL
SELECT 'agents', count(*) FROM agents;
EOF
# Atau jika Docker:
# docker compose exec postgres psql -U multica -d multica -c "SELECT 'workspaces' as table, count(*) FROM workspaces UNION ALL SELECT 'members', count(*) FROM members UNION ALL SELECT 'issues', count(*) FROM issues UNION ALL SELECT 'agents', count(*) FROM agents;"
```

Pastikan jumlah data sama dengan server lama.

### Langkah 6.5 — Restore File Upload (jika pakai local storage)

```bash
# Di server baru, sebagai user multica
sudo su - multica
cd /home/multica/multica

# Buat folder jika belum ada
mkdir -p data/uploads

# Extract upload backup
tar -xzf /tmp/multica-uploads.tar.gz -C data/

# Verifikasi
ls -la data/uploads/ | head -10
du -sh data/uploads/
```

Atau jika menggunakan `rsync` (Langkah 5.2), file sudah ada di lokasinya.

### Langkah 6.6 — Restore Daemon Config (Opsional)

```bash
# Di server baru, sebagai user multica
sudo su - multica

# Extract daemon config
if [ -f /tmp/multica-daemon-config.tar.gz ]; then
  tar -xzf /tmp/multica-daemon-config.tar.gz -C ~
  ls -la ~/.config/multica/
fi
```

> Jika server URL berubah, daemon config perlu di-update (lihat Langkah 8.3).
> Sering kali lebih mudah re-login: `multica setup self-host`.

### Langkah 6.7 — Jalankan Migration (jika ada schema baru)

Jika server baru menggunakan versi code yang lebih baru dari server lama,
mungkin ada migration baru yang belum di-apply:

```bash
# Di server baru, sebagai user multica
cd /home/multica/multica
export $(grep -v '^#' .env | xargs)

# Jalankan migration (hanya apply yang belum ada)
./server/bin/migrate up
```

> Migration idempotent — hanya apply migration yang belum ada di database.
> Jika database dari server lama sudah up-to-date, tidak ada perubahan.

### Langkah 6.8 — Cleanup File Backup Sementara

```bash
# Di server baru
rm -f /tmp/multica-db-backup.dump
rm -f /tmp/multica-uploads.tar.gz
rm -f /tmp/multica-env-backup
rm -f /tmp/multica-daemon-config.tar.gz

# Di server lama (setelah verifikasi server baru berjalan)
rm -f /tmp/multica-db-backup.dump
rm -f /tmp/multica-uploads.tar.gz
rm -f /tmp/multica-env-backup
rm -f /tmp/multica-daemon-config.tar.gz
```

---

## 7. Verifikasi & Cutover

### Langkah 7.1 — Start Services di Server Baru

```bash
# Di server baru (sebagai sudo user)
sudo systemctl start multica-backend
sudo systemctl start multica-frontend

# Cek status
sudo systemctl status multica-backend
sudo systemctl status multica-frontend
# Harus: active (running)
```

### Langkah 7.2 — Verifikasi Backend

```bash
# Health check
curl http://localhost:8080/health
# {"status":"ok"}

# Readiness check (cek DB + migration)
curl http://localhost:8080/readyz
# {"status":"ok","checks":{"db":"ok","migrations":"ok"}}

# Cek log tidak ada error
sudo journalctl -u multica-backend --since "5 min ago" | grep -i error
# Harus kosong
```

### Langkah 7.3 — Verifikasi Frontend

```bash
# Cek frontend respond
curl -s http://localhost:3000 | head -5
# Harus ada HTML output

# Cek log tidak ada error
sudo journalctl -u multica-frontend --since "5 min ago" | grep -i error
# Harus kosong
```

### Langkah 7.4 — Verifikasi Data via API

```bash
# Login dengan akun yang ada di server lama
# (JWT_SECRET sama, jadi session lama seharusnya masih valid)

# Cek API endpoint penting
curl -s http://localhost:8080/api/config | python3 -m json.tool
# Harus return config instance

# Cek workspace list (butuh auth token)
# Login via browser → cek apakah workspace dari server lama muncul
```

### Langkah 7.5 — Verifikasi via Browser

1. Buka `http://<IP-SERVER-BARU>:3000` di browser
2. Login dengan email yang ada di server lama
3. Cek:
   - ✅ Workspace list muncul (sama dengan server lama)
   - ✅ Issues muncul dengan data lengkap
   - ✅ Members/agents terdaftar
   - ✅ File attachment bisa di-download (jika pakai local storage)
   - ✅ WebSocket connected (real-time update bekerja)

### Langkah 7.6 — Cutover Berhasil

Jika semua verifikasi di atas lulus, **cutover berhasil**. Downtime selesai.

```bash
# Catat waktu cutover
date
# Output: Wed Jul 15 10:30:00 UTC 2026
```

---

## 8. Post-Migration: Update DNS & Client

### Langkah 8.1 — Update DNS (jika pakai domain)

Jika Multica diakses via domain (bukan IP langsung), update DNS record:

```
# Di DNS provider kamu
# A record:
multica.example.com  →  <IP-SERVER-BARU>

# Atau CNAME:
multica.example.com  →  server-baru.example.com
```

> DNS propagation butuh waktu beberapa menit sampai jam. Cek dengan:
> ```bash
> dig multica.example.com +short
> # Atau
> nslookup multica.example.com
> ```

### Langkah 8.2 — Update `.env` dengan Domain Baru (jika pakai domain)

```bash
# Di server baru
sudo su - multica
cd /home/multica/multica
nano .env
```

Update:

```env
FRONTEND_ORIGIN=https://multica.example.com
MULTICA_APP_URL=https://multica.example.com
CORS_ALLOWED_ORIGINS=https://multica.example.com
GOOGLE_REDIRECT_URI=https://multica.example.com/auth/callback
```

Restart services:

```bash
exit
sudo systemctl restart multica-backend
sudo systemctl restart multica-frontend
```

### Langkah 8.3 — Update CLI Daemon

Jika daemon berjalan di mesin user (bukan di server), update URL server:

```bash
# Di mesin user
multica config set server_url http://<IP-ATAU-DOMAIN-SERVER-BARU>:8080
multica config set app_url http://<IP-ATAU-DOMAIN-SERVER-BARU>:3000

# Restart daemon
multica daemon stop
multica daemon start

# Verifikasi
multica daemon status
```

Atau jika daemon berjalan di server baru itu sendiri:

```bash
# Di server baru, sebagai user multica
multica config set server_url http://localhost:8080
multica config set app_url http://localhost:3000
multica daemon stop
multica daemon start
multica daemon status
```

### Langkah 8.4 — Update Reverse Proxy (jika pakai nginx/Caddy)

Jika ada reverse proxy di depan Multica, update upstream ke server baru:

```nginx
# Contoh nginx config
upstream multica_backend {
    server <IP-SERVER-BARU>:8080;
}

upstream multica_frontend {
    server <IP-SERVER-BARU>:3000;
}
```

Reload nginx:

```bash
sudo nginx -t
sudo systemctl reload nginx
```

### Langkah 8.5 — Notifikasi User

Informasikan ke user:
- URL baru Multica (jika IP/domain berubah)
- Mereka mungkin perlu login ulang jika browser tidak menyimpan session
- Jika `JWT_SECRET` dipertahankan, session lama seharusnya masih valid

---

## 9. Rollback (jika gagal)

Jika migrasi gagal dan perlu kembali ke server lama:

### Langkah 9.1 — Stop Services di Server Baru

```bash
# Di server baru
sudo systemctl stop multica-frontend
sudo systemctl stop multica-backend
```

### Langkah 9.2 — Start Services di Server Lama

```bash
# Di server lama
sudo systemctl start multica-backend
sudo systemctl start multica-frontend

# Verifikasi
sudo systemctl status multica-backend
sudo systemctl status multica-frontend
```

### Langkah 9.3 — Verifikasi Server Lama Berjalan

```bash
curl http://localhost:8080/health
# {"status":"ok"}
```

> ⚠️ **Catatan**: Data yang dibuat di server baru setelah cutover akan hilang
> saat rollback. Jika ada data baru, backup dulu dari server baru sebelum
> rollback.

### Langkah 9.4 — Investigasi Penyebab Gagal

Cek log di server baru:

```bash
sudo journalctl -u multica-backend --since "30 min ago"
sudo journalctl -u multica-frontend --since "30 min ago"
```

Perbaiki issue, lalu ulangi migrasi dari Langkah 4.

---

## 10. Troubleshooting Migrasi

### `pg_dump` gagal: "permission denied for table"

```bash
# Pastikan user punya akses read ke semua tabel
# Jika Docker
docker compose exec postgres psql -U multica -d multica -c "GRANT SELECT ON ALL TABLES IN SCHEMA public TO multica;"

# Jika native
sudo -u postgres psql -d multica -c "GRANT SELECT ON ALL TABLES IN SCHEMA public TO multica;"
```

### `pg_restore` gagal: "extension vector does not exist"

```bash
# Pastikan pgvector terinstall di server baru
# Jika Docker — sudah include di image pgvector/pgvector:pg17
# Jika native:
sudo apt install -y postgresql-17-pgvector

# Buat extension di database baru
psql -U multica -d multica -h localhost -c "CREATE EXTENSION IF NOT EXISTS vector;"
# Atau jika Docker:
docker compose exec postgres psql -U multica -d multica -c "CREATE EXTENSION IF NOT EXISTS vector;"
```

### `pg_restore` gagal: "role multica does not exist"

```bash
# Pastikan user multica ada di PostgreSQL server baru
# Jika Docker — sudah ada di image
# Jika native:
sudo -u postgres psql -c "CREATE USER multica WITH PASSWORD '<password>';"
```

### `pg_restore` warning: "errors ignored on restore"

```bash
# Cek error detail
pg_restore --dbname=multica --no-owner --no-privileges /tmp/multica-db-backup.dump 2>&1 | grep ERROR

# Biasanya warning (bukan error) — object sudah ada, di-skip
# Jika ada error real, cek apakah schema cocok
```

### Database restore berhasil tapi data tidak muncul di UI

```bash
# 1. Cek backend connect ke database yang benar
cat /home/multica/multica/.env | grep DATABASE_URL
# Pastikan host, port, database name benar

# 2. Cek backend log
sudo journalctl -u multica-backend -f
# Cek error "no such table" atau "permission denied"

# 3. Test query manual
psql -U multica -d multica -h localhost -c "SELECT count(*) FROM workspaces;"
# Atau jika Docker:
# docker compose exec postgres psql -U multica -d multica -c "SELECT count(*) FROM workspaces;"
```

### File upload tidak bisa di-download setelah migrasi

```bash
# 1. Cek file ada di lokasi yang benar
ls -la /home/multica/multica/data/uploads/
# Bandingkan dengan server lama

# 2. Cek permission
sudo chown -R multica:multica /home/multica/multica/data/
chmod -R 755 /home/multica/multica/data/

# 3. Cek LOCAL_UPLOAD_DIR di .env
grep LOCAL_UPLOAD_DIR /home/multica/multica/.env
# Harus: ./data/uploads

# 4. Cek LOCAL_UPLOAD_BASE_URL
grep LOCAL_UPLOAD_BASE_URL /home/multica/multica/.env
# Jika kosong, default ke http://localhost:8080
# Jika server baru punya domain, set:
# LOCAL_UPLOAD_BASE_URL=http://<IP-ATAU-DOMAIN-SERVER-BARU>:8080
```

### WebSocket tidak connect setelah migrasi

```bash
# 1. Cek CORS_ALLOWED_ORIGINS di .env
grep CORS_ALLOWED_ORIGINS /home/multica/multica/.env
# Pastikan origin frontend baru terdaftar

# 2. Jika akses dari domain berbeda, tambahkan:
# CORS_ALLOWED_ORIGINS=https://multica.example.com,http://<IP-SERVER-BARU>:3000

# 3. Restart backend
sudo systemctl restart multica-backend
```

### Session user invalid setelah migrasi (semua harus login ulang)

```bash
# Penyebab: JWT_SECRET berbeda antara server lama dan baru
# Cek JWT_SECRET di kedua server
# Server lama:
grep JWT_SECRET /home/multica/multica/.env  # (di server lama)
# Server baru:
grep JWT_SECRET /home/multica/multica/.env  # (di server baru)

# Jika berbeda, copy JWT_SECRET dari server lama ke server baru
# Lalu restart backend
sudo systemctl restart multica-backend
```

### Daemon tidak connect ke server baru

```bash
# Cek config daemon
multica config get server_url
# Harus: http://<IP-ATAU-DOMAIN-SERVER-BARU>:8080

# Jika masih ke server lama, update:
multica config set server_url http://<IP-ATAU-DOMAIN-SERVER-BARU>:8080
multica config set app_url http://<IP-ATAU-DOMAIN-SERVER-BARU>:3000

# Re-login jika auth token expired
multica login

# Restart daemon
multica daemon stop
multica daemon start
```

### Transfer file gagal: "connection refused"

```bash
# 1. Cek SSH berjalan di server baru
sudo systemctl status sshd
sudo systemctl start sshd

# 2. Cek firewall tidak block SSH
sudo ufw status
sudo ufw allow ssh

# 3. Test SSH manual
ssh multica@<IP-SERVER-BARU>
```

### Transfer file lambat (file upload besar)

```bash
# Gunakan rsync dengan compression dan resume
rsync -avz --progress --partial \
  /home/multica/multica/data/uploads/ \
  multica@$NEW_SERVER_IP:/home/multica/multica/data/uploads/

# Atau kompres lebih agresif
rsync -avz --progress --partial --compress-level=9 \
  /home/multica/multica/data/uploads/ \
  multica@$NEW_SERVER_IP:/home/multica/multica/data/uploads/
```

---

## Ringkasan Alur Migrasi (Checklist)

### Persiapan

```
[ ] 1.  Server baru sudah terinstall Multica (sampai build, sebelum migration)
[ ] 2.  PostgreSQL berjalan di server baru
[ ] 3.  Database kosong dibuat di server baru
[ ] 4.  Catat IP/hostname server baru
```

### Backup (Server Lama) — Downtime Mulai

```
[ ] 5.  sudo systemctl stop multica-frontend multica-backend
[ ] 6.  pg_dump database → /tmp/multica-db-backup.dump
[ ] 7.  tar upload folder → /tmp/multica-uploads.tar.gz (jika local storage)
[ ] 8.  cp .env → /tmp/multica-env-backup
[ ] 9.  tar daemon config → /tmp/multica-daemon-config.tar.gz (opsional)
[ ] 10. Verifikasi semua backup
```

### Transfer

```
[ ] 11. scp/rsync backup files ke server baru
[ ] 12. Verifikasi file sampai di server baru
```

### Restore (Server Baru)

```
[ ] 13. cp /tmp/multica-env-backup .env → edit untuk server baru
[ ] 14. PERTAHANKAN JWT_SECRET dari server lama (jangan generate baru!)
[ ] 15. pg_restore database dari dump
[ ] 16. Verifikasi data restore (count tabel)
[ ] 17. Extract upload backup → data/uploads/
[ ] 18. Extract daemon config (opsional)
[ ] 19. ./server/bin/migrate up (jika ada migration baru)
[ ] 20. Cleanup file backup dari /tmp
```

### Cutover — Downtime Selesai

```
[ ] 21. sudo systemctl start multica-backend multica-frontend
[ ] 22. curl http://localhost:8080/health → {"status":"ok"}
[ ] 23. curl http://localhost:3000 → HTML output
[ ] 24. Login via browser → verifikasi data muncul
[ ] 25. Verifikasi WebSocket connected
```

### Post-Migration

```
[ ] 26. Update DNS (jika pakai domain)
[ ] 27. Update .env dengan domain baru (jika perlu) → restart services
[ ] 28. Update CLI daemon: multica config set server_url + app_url
[ ] 29. Update reverse proxy (jika ada)
[ ] 30. Notifikasi user URL baru
```

### Cleanup Server Lama (setelah 1-7 hari stabil)

```
[ ] 31. Verifikasi server baru stabil minimal 24 jam
[ ] 32. Stop services di server lama: sudo systemctl stop multica-* 
[ ] 33. Disable services di server lama: sudo systemctl disable multica-*
[ ] 34. Backup terakhir server lama (jaga-jaga)
[ ] 35. Decommission server lama
```

---

## Tips Migrasi Aman

### 1. Test Migrasi Sebelum Production

Jika memungkinkan, lakukan dry-run migrasi:
- Backup di server lama
- Restore di server baru (staging)
- Verifikasi data
- Hapus data staging
- Baru lakukan migrasi production

### 2. Migrasi Bertahap (jika downtime harus minimal)

Jika downtime harus sangat singkat:

```bash
# SEBELUM downtime: transfer file upload (bisa berjalan saat server lama aktif)
rsync -avz --progress \
  /home/multica/multica/data/uploads/ \
  multica@$NEW_SERVER_IP:/home/multica/multica/data/uploads/

# SAAT downtime: hanya backup + transfer + restore database
# Ini yang butuh server berhenti (untuk konsistensi data)

# SETELAH cutover: sync ulang file upload yang berubah
rsync -avz --progress --delete \
  /home/multica/multica/data/uploads/ \
  multica@$NEW_SERVER_IP:/home/multica/multica/data/uploads/
```

### 3. Pertahankan JWT_SECRET

**Paling penting**: `JWT_SECRET` harus sama antara server lama dan baru agar
session user tetap valid. Jika diganti, semua user harus login ulang.

### 4. Backup Server Lama Jangan Dihapus

Setelah migrasi, jaga server lama tetap berjalan (atau minimal database-nya
tersimpan) selama 1-7 hari sebagai fallback jika ada masalah di server baru.

### 5. Monitor Setelah Migrasi

```bash
# Monitor log 24 jam pertama
sudo journalctl -u multica-backend -f
sudo journalctl -u multica-frontend -f

# Cek error
sudo journalctl -u multica-backend --since "24 hours ago" | grep -i error
```

---

## Referensi

| Dokumen | Isi |
|---------|-----|
| [installation-bare-metal-home.md](installation-bare-metal-home.md) | Panduan instalasi bare metal di `/home/multica` |
| [installation-bare-metal.md](installation-bare-metal.md) | Panduan instalasi bare metal di `/opt/multica` |
| [SELF_HOSTING.md](../SELF_HOSTING.md) | Panduan self-hosting via Docker |
| [SELF_HOSTING_ADVANCED.md](../SELF_HOSTING_ADVANCED.md) | Konfigurasi advanced (reverse proxy, S3, database) |
| [CLI_AND_DAEMON.md](../CLI_AND_DAEMON.md) | Referensi CLI & daemon commands |