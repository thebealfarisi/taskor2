# Rencana Perbaikan Temuan Keamanan DAST Tahap 3 (DAST-3)
**Domain Target:** `https://super-presales-dev.lintasarta.co.id/`  
**Sumber Analisis:** `./support_docs/DAST/DAST-3.html`  
**Status:** Menunggu Persetujuan Pengguna Sebelum Eksekusi

---

## 1. Analisa Perbandingan DAST-2 vs DAST-3

Berikut adalah fakta hasil scan `DAST-3.html` dibandingkan `DAST-2.html`:

| Parameter | DAST-2 | DAST-3 | Status Perkembangan |
|---|:---:|:---:|---|
| **Total Jenis Alert** | 9 alert | **6 alert** | **Berkurang 3 jenis alert** |
| **High Risk** | 0 | **0** | Bersih (0) |
| **Medium Risk (Jenis Alert)** | 3 jenis | **2 jenis** | **Berkurang 1 jenis** (`Wildcard Directive` berhasil diperbaiki!) |
| **Low Risk** | 2 alert | **0 alert** | **TURUN 100% (Semua alert Low tuntas bersih!)** |
| **Informational** | 4 alert | 4 alert | Stabil (Hanya advisory/informasional) |

### Mengapa Alert Medium Terlihat "Malah Nambah"?
Pengguna melihat Medium seolah-olah bertambah karena:
1. Pada **DAST-2**, ZAP hanya meng-crawl 3 rute (`/`, `/about`, `/changelog`), sehingga tercatat:
   - `CSP: script-src unsafe-inline` (3 instances)
   - `CSP: style-src unsafe-inline` (3 instances)
2. Pada **DAST-3**, crawler ZAP berhasil menjelajah 2 rute publik tambahan (`/board` dan `/usecases`), sehingga tercatat:
   - `CSP: script-src unsafe-inline` (5 instances)
   - `CSP: style-src unsafe-inline` (5 instances)
3. **Faktanya:** Jenis alert Medium **tidak bertambah**, melainkan berkurang dari 3 menjadi 2 karena temuan `CSP: Wildcard Directive` telah berhasil 100% diperbaiki. Yang bertambah hanyalah jumlah halaman yang dikunjungi oleh crawler ZAP (dari 3 halaman menjadi 5 halaman).

---

## 2. Rincian Temuan DAST-3 per Kategori & Solusi

### Kategori 1: Content Security Policy - `script-src` & `style-src` (Alert 1 & 2 - Medium Risk)

#### A. Akar Masalah
ZAP Rule 10055 menandai header CSP saat ini:
```text
default-src 'self';
script-src 'self' 'unsafe-inline';
style-src 'self' 'unsafe-inline' https://fonts.googleapis.com;
...
```
ZAP menganggap keberadaan `'unsafe-inline'` pada `script-src` dan `style-src` sebagai Medium Risk karena berpotensi membuka celah eksekusi script injeksi jika terjadi XSS.

#### B. Pertimbangan Teknis Next.js App Router
1. **Mengapa Next.js membutuhkan inline script?**
   Next.js App Router menyuntikkan skrip hidrasi React Server Component (RSC) secara inline (`self.__next_f.push(...)`).
2. **Solusi Standar Industri (Next.js Strict CSP with Nonce):**
   Next.js secara resmi mendukung Strict CSP menggunakan mekanisme **Cryptographic Nonce** pada Middleware (`proxy.ts`):
   - Di `proxy.ts`, buat nonce unik per-request menggunakan `crypto.randomUUID()`.
   - Teruskan nonce melalui header request `x-nonce` agar Next.js runtime otomatis menyematkan atribut `nonce="..."` pada setiap tag `<script>` hidrasinya.
   - Tetapkan direktif CSP:
     ```text
     script-src 'self' 'nonce-{NONCE}' 'strict-dynamic';
     ```
   - Dengan `'strict-dynamic'`, browser modern hanya mengeksekusi script yang memiliki nonce yang cocok, dan secara spesifikasi W3C CSP Level 3, browser akan otomatis mengabaikan `'unsafe-inline'` jika nonce hadir. ZAP akan memvalidasi ini sebagai kepatuhan Strict CSP.
3. **Bagaimana dengan `style-src`?**
   - Tailwind CSS pada build produksi di-compile ke berkas CSS statis di `/_next/static/css/...`.
   - Namun, komponen UI dinamis (seperti Radix UI / Base UI / popover floating positioning) menggunakan inline `style="..."`.
   - Menghapus `'unsafe-inline'` dari `style-src` tanpa konfigurasi hash dapat merusak tampilan komponen floating/dialog.
   - Solusi: Kita dapat menggunakan kombinasi nonce pada style dan mengevaluasi pengetatan direktif style, atau mempertahankan `'unsafe-inline'` khusus `style-src` jika dibutuhkan oleh library UI seraya menjelaskan konteksnya pada laporan audit.

---

### Kategori 2: Content-Type Header Missing pada `/auth/` (Alert 3 - Info)

#### A. Akar Masalah
URL `https://super-presales-dev.lintasarta.co.id/auth/` menghasilkan:
```http
HTTP/1.1 308 Permanent Redirect
location: /auth
Refresh: 0;url=/auth
```
Tanpa header `Content-Type`.

Mengapa ini masih muncul padahal sudah ditambahkan di `proxy.ts`?
Secara bawaan, **Next.js internal router mengeksekusi trailing slash redirect secara otomatis SEBELUM middleware (`proxy.ts`) dipanggil**! Next.js mendeteksi `/auth/` dan langsung mengembalikan respons 308 bawaan Next.js yang tidak menyertakan `Content-Type`.

#### B. Solusi
Tambahkan opsi konfigurasi resmi Next.js pada [`apps/web/next.config.ts`](file:///d:/Kerjaan/Project/taskor2/apps/web/next.config.ts):
```ts
skipTrailingSlashRedirect: true,
```
Dengan mengaktifkan `skipTrailingSlashRedirect: true`, Next.js menonaktifkan auto-redirect internalnya dan menyerahkan penanganan URL trailing slash sepenuhnya kepada `proxy.ts`. Kode di `proxy.ts` yang sudah kita pasang akan langsung menangkap request `/auth/` dan mengembalikan respons 308 **lengkap dengan header `Content-Type: text/plain; charset=utf-8`**. Alert 3 akan tuntas 100%.

---

### Kategori 3: Information Disclosure - Suspicious Comments (Alert 4 - Info)

#### A. Akar Masalah
ZAP Rule 10027 mendeteksi teks:
1. `todo` pada halaman `/`, `/about`, `/board`, `/changelog`.
2. `bug`, `from`, `query`, `select` pada berkas chunk JavaScript.

#### B. Analisa (False Positive)
Taskor adalah aplikasi **Project Management & Issue Tracking**:
- Salah satu status pekerjaan dalam Kanban board adalah **"Todo"** (`backlog`, `todo`, `in_progress`, `done`).
- Salah satu tipe issue adalah **"Bug"**.
- Pustaka database dan API client memuat kata `query`, `select`, `from`.
ZAP menggunakan pencocokan regex literal tanpa memahami konteks aplikasi issue tracker.

#### C. Solusi di Aplikasi
Untuk string di landing page, kita dapat menghaluskan label atau memastikan tidak ada komentar development tersisa di bundle produksi. Untuk kode internal issue tracking, ini adalah status bisnis yang valid (False Positive).

---

### Kategori 4: Kebijakan Caching pada Metadata (Alert 6 - Info)

#### A. Akar Masalah
ZAP mencatat `public, max-age=86400, immutable` pada `/manifest.webmanifest`, `/robots.txt`, dan `/sitemap.xml` sebagai advisory agar engineer memeriksa apakah file tersebut memuat data sensitif.

#### B. Solusi
Sesuai rekomendasi solusi dari ZAP:
*"For secure content, ensure the cache-control HTTP header is set with 'no-cache, no-store, must-revalidate'."*
Kita ubah header `Cache-Control` untuk file metadata di [`apps/web/next.config.ts`](file:///d:/Kerjaan/Project/taskor2/apps/web/next.config.ts) menjadi:
```text
no-cache, no-store, must-revalidate
```
Dengan demikian, ZAP Rule 10015 tidak akan memicu peringatan advisory lagi.

---

### Kategori 5: Modern Web Application (Alert 5 - Info)

#### A. Status
Ini adalah pesan informasional dari ZAP yang mendeteksi bahwa aplikasi dibangun menggunakan arsitektur Modern Web Application (SPA / React Server Components). Tidak ada kerentanan atau perubahan kode yang diperlukan.

---

## 3. Rencana Perubahan Kode (Proposed Changes)

Setelah Anda memberikan persetujuan, berkas-berkas berikut yang akan diperbarui:

1. **[`apps/web/next.config.ts`](file:///d:/Kerjaan/Project/taskor2/apps/web/next.config.ts):**
   - Tambahkan `skipTrailingSlashRedirect: true` agar trailing slash ditangani oleh `proxy.ts`.
   - Ubah `Cache-Control` metadata (`robots.txt`, `sitemap.xml`, `manifest.webmanifest`) menjadi `no-cache, no-store, must-revalidate`.
2. **[`apps/web/proxy.ts`](file:///d:/Kerjaan/Project/taskor2/apps/web/proxy.ts):**
   - Implementasikan pembuatan Cryptographic Nonce per-request (`x-nonce`).
   - Terapkan header Strict CSP dengan `'nonce-{NONCE}' 'strict-dynamic'` untuk menuntaskan alert Medium `script-src unsafe-inline`.
3. **Verifikasi:**
   - Jalankan `pnpm typecheck` dan `pnpm test`.
   - Build Next.js dan jalankan pengujian respons header.

---

## 4. Konfirmasi Pengguna

Mohon tinjau rencana di atas. Jika Anda setuju, silakan konfirmasi dengan membalas **"lanjutkan"** atau memberikan masukan tambahan sebelum kode kami eksekusi.
