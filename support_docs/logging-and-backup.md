# Panduan Backup, Export Log, dan Preservasi Percakapan — Multica

Dokumen ini berisi panduan lengkap untuk melakukan manajemen log, backup database, serta cara mengunduh/merekap log percakapan pengguna dan eksekusi Agent AI ke server lokal.

---

## 1. Arsitektur Penyimpanan Log & Percakapan

Secara bawaan (*default*), seluruh data percakapan dan aktivitas pada Multica tersimpan di **PostgreSQL** (`multica` database):

| Jenis Log / Data | Tabel Database | Deskripsi Isi |
|---|---|---|
| **Percakapan User & Komentar** | `comment` | Pesan teks pengguna di tiket/issue, komentar, dan tanggapan. (`content`, `author_id`, `created_at`) |
| **Log Eksekusi & Tool Call Agent** | `task_message` | Langkah eksekusi Agent AI, perintah terminal (*tool call*), argumen input, dan output teks. |
| **Sesi Chatbox** | `chat_session`, `chat_message` | Sesi percakapan langsung via UI chatbox. |
| **Audit Trail & Aktivitas** | `activity_log` | Catatan aktivitas sistem, perubahan status, dan rekam jejak pengguna. |

---

## 2. Cara Export Percakapan ke File Teks / CSV

Jika Anda ingin mengekstrak teks percakapan pengguna atau riwayat tindakan Agent AI dari database ke dalam file teks (*plain text*) di server lokal:

### A. Export Teks Percakapan Pengguna & Komentar
Jalankan perintah berikut di terminal server (menggunakan kontainer Docker PostgreSQL `multica-postgres-1`):

```bash
docker exec -i multica-postgres-1 psql -U multica -d multica -c "
SELECT 
    created_at AS waktu, 
    author_type AS tipe_pengirim, 
    author_id AS pengirim_id, 
    content AS isi_percakapan 
FROM comment 
ORDER BY created_at ASC;
" > /home/multica/log_percakapan_user.txt
```

Untuk melihat 30 baris awal file hasil export:
```bash
cat /home/multica/log_percakapan_user.txt | head -n 30
```

### B. Export Riwayat Perintah & Output Agent AI (`task_message`)
Untuk melihat seluruh perintah terminal (*tool call*), input, dan balasan yang dihasilkan Agent AI:

```bash
docker exec -i multica-postgres-1 psql -U multica -d multica -c "
SELECT 
    created_at AS waktu, 
    type AS tipe_pesan, 
    tool AS tool_digunakan, 
    content AS pesan, 
    input AS argumen_input, 
    output AS hasil_output 
FROM task_message 
ORDER BY created_at ASC;
" > /home/multica/log_eksekusi_agent.txt
```

---

## 3. Backup Database PostgreSQL Lengkap

Untuk mengamankan seluruh database (termasuk percakapan, user, workspace, dan status agent) ke file `.dump` terkompresi di server lokal:

### A. Perintah Backup Manual
```bash
mkdir -p /home/multica/backup_data

set -a; source /opt/multica/.env; set +a
docker exec -t multica-postgres-1 pg_dump -U "${POSTGRES_USER:-multica}" -d "${POSTGRES_DB:-multica}" -Fc > /home/multica/backup_data/multica-db-$(date +%Y%m%d_%H%M%S).dump
```

### B. Otomatisasi Backup Harian (Cron Job)
Tambahkan perintah ke `crontab` agar backup berjalan otomatis setiap jam 00:00:

```bash
crontab -e
```
*Isi dibagian paling bawah:*
```cron
0 0 * * * set -a; source /opt/multica/.env; set +a; docker exec -t multica-postgres-1 pg_dump -U "${POSTGRES_USER:-multica}" -d "${POSTGRES_DB:-multica}" -Fc > /home/multica/backup_data/multica-db-$(date +%Y%m%d).dump
```

### C. Cara Restore Database dari Dump
```bash
set -a; source /opt/multica/.env; set +a
docker exec -i multica-postgres-1 pg_restore -U "${POSTGRES_USER:-multica}" -d "${POSTGRES_DB:-multica}" --clean < /home/multica/backup_data/multica-db-YYYYMMDD.dump
```

---

## 4. Manajemen Application Log Realtime (Systemd & Journalctl)

Log aktivitas backend Go (`multica-backend`) dan frontend Next.js (`multica-frontend`) dicatat oleh `systemd`.

### A. Export Log Aplikasi ke File
```bash
mkdir -p /home/multica/app_logs

# Export log backend
sudo journalctl -u multica-backend --no-pager -n 50000 > /home/multica/app_logs/backend-activity.log

# Export log frontend
sudo journalctl -u multica-frontend --no-pager -n 50000 > /home/multica/app_logs/frontend-activity.log
```

### B. Mengalihkan Output Log Secara Otomatis ke File Disk
Jika Anda ingin log aplikasi ditulis langsung ke file disk secara kontinu:

1. Edit file unit service backend:
   ```bash
   sudo nano /etc/systemd/system/multica-backend.service
   ```
2. Tambahkan variabel berikut pada blok `[Service]`:
   ```ini
   StandardOutput=append:/home/multica/app_logs/backend.log
   StandardError=append:/home/multica/app_logs/backend-error.log
   ```
3. Reload systemd & restart service:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl restart multica-backend
   ```
