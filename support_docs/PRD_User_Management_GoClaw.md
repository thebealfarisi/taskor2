# Product Requirements Document (PRD): User Management GoClaw

**Dokumen ini dibuat khusus agar mudah dipahami oleh programmer pemula yang akan mengerjakan fitur ini.**

---

## 1. Ringkasan Fitur
Fitur "User Management GoClaw" adalah sebuah halaman web untuk mengelola data pemetaan user (email, username, dan token) pada sistem GoClaw. Halaman ini menggunakan konsep *Single Page Application* (satu halaman interaktif tanpa perlu berpindah-pindah/reload halaman penuh) untuk mendukung proses operasi CRUD (Create, Read, Update, Delete).

---

## 2. Struktur Database
Data akan disimpan dalam tabel bernama `user_mapping_goclaw` dengan rancangan awal sebagai berikut:

```sql
CREATE TABLE user_mapping_goclaw (
    email VARCHAR(255) NOT NULL,
    username VARCHAR(100) NOT NULL,
    token TEXT NOT NULL
);
```

> [!WARNING] Saran Perbaikan Struktur Tabel
> Pada rancangan di atas, tabel tidak memiliki *Primary Key*. Untuk mempermudah dan mengamankan proses Edit (Update) dan Hapus (Delete), **sangat disarankan untuk menambahkan kolom ID (Primary Key)** (misalnya kolom `id` dengan fitur auto increment/otomatis bertambah). Jika tidak ada parameter unik seperti ID, sistem memiliki risiko salah sasaran saat menghapus atau mengubah data spesifik (misal, jika ada dua baris dengan email dan username yang sama).

---

## 3. Kebutuhan Tampilan (User Interface)

Seluruh aktivitas dikemas dalam satu halaman utama. Berikut adalah elemen yang harus ada:

1. **Judul Halaman**: "User Management GoClaw"
2. **Tombol "Tambah Data"**: Diletakkan di atas atau di samping tabel data. Jika diklik, akan memunculkan Pop-up (Modal Form).
3. **Tabel Data**: Terdiri dari 4 kolom utama:
   - Email
   - Username
   - Token
   - Aksi (Berisi dua tombol: **Edit** dan **Delete**)
4. **Pop-up Form (Modal) - Untuk Tambah & Edit Data**:
   - **Input Email**: Kotak isian teks biasa.
   - **Input Username**: Pilihan *Dropdown* (Select box). Pilihan diambil dari data username yang sudah ada.
   - **Input Token**: Kotak isian teks panjang (Textarea atau Text input).
   - Tombol "Simpan" dan tombol "Batal" (untuk menutup pop-up).
5. **Pop-up Konfirmasi Hapus**: Dialog peringatan yang muncul saat pengguna mengklik tombol "Delete" (contoh: "Apakah Anda yakin ingin menghapus data ini?").

---

## 4. Alur Logika (Fungsionalitas CRUD)

### A. READ (Membaca & Menampilkan Data ke Tabel)
1. Saat halaman dibuka, aplikasi (frontend) akan meminta data daftar pemetaan user ke database (melalui backend).
2. Data yang didapat langsung ditampilkan baris per baris pada tabel.
3. Di belakang layar, aplikasi juga akan meminta **daftar Username secara unik** (`SELECT DISTINCT username FROM user_mapping_goclaw`). Data ini akan disimpan sementara untuk digunakan sebagai pilihan pada *Dropdown* Username saat form Tambah/Edit dibuka.
4. *Catatan Logika: Jika tabel di database masih benar-benar kosong, aplikasi tidak akan memiliki referensi dropdown username. Untuk penggunaan pertama kali, admin/programmer harus memasukkan (insert) data pancingan secara manual langsung ke database (backdoor) agar pilihan username di dropdown muncul.*

### B. CREATE (Menambah Data Baru)
1. Pengguna mengklik tombol "Tambah Data".
2. Halaman menampilkan Pop-up form dalam kondisi kosong. Pilihan dropdown `username` sudah terisi dengan opsi dari data yang sudah ada.
3. Pengguna mengisi Email, memilih Username dari dropdown, dan mengetikkan Token.
4. Pengguna menekan tombol "Simpan".
5. Aplikasi melakukan **validasi** untuk memastikan tidak ada input yang dibiarkan kosong. Jika ada yang kosong, tampilkan pesan error.
6. Jika valid, data dikirim untuk disimpan ke database (`INSERT`).
7. Saat sukses disimpan: Pop-up otomatis tertutup, tabel memuat ulang (refresh) untuk memunculkan data terbaru di baris paling akhir/awal, dan muncul pesan notifikasi "Data berhasil ditambahkan".

### C. UPDATE (Mengubah Data)
1. Pengguna melihat tabel dan memutuskan untuk mengubah suatu baris data, lalu mengklik tombol "Edit" pada baris tersebut.
2. Halaman menampilkan Pop-up form yang **sudah terisi data lama** sesuai baris yang dipilih.
3. Pengguna mengubah data (misalnya mengubah Token atau Email).
4. Pengguna menekan tombol "Simpan".
5. Sama seperti proses tambah, aplikasi memvalidasi form yang kosong.
6. Jika valid, data yang baru dikirim untuk menimpa data lama di database (`UPDATE` berdasarkan penanda unik/ID dari data yang sedang diedit).
7. Saat sukses diubah: Pop-up otomatis tertutup, tabel data di-refresh agar menampilkan perubahan terbaru, dan muncul pesan notifikasi "Data berhasil diubah".

### D. DELETE (Menghapus Data)
1. Pengguna mengklik tombol "Delete" pada salah satu baris di tabel.
2. Muncul pop-up konfirmasi (agar tidak langsung terhapus bila tidak sengaja terpencet).
3. Jika pengguna memilih "Batal/Tidak", operasi dihentikan dan pop-up tertutup.
4. Jika pengguna memilih "Ya/Hapus", sistem akan mengirim perintah hapus data ke database (`DELETE` berdasarkan penanda unik/ID).
5. Saat sukses dihapus: Tabel otomatis di-refresh untuk menghilangkan baris yang baru dihapus tersebut, lalu muncul pesan notifikasi "Data berhasil dihapus".
