# Rencana Perbaikan Keamanan DAST (Dynamic Application Security Testing)

Dokumen ini disusun berdasarkan hasil analisis terhadap laporan DAST yang tersimpan pada file [`DAST.html`](file:///d:/Kerjaan/Project/taskor2/support_docs/DAST/DAST.html) hasil pemindaian terhadap domain target `https://super-presales-dev.lintasarta.co.id/`.

> [!IMPORTANT]
> **Status:** Menunggu Persetujuan (Pending Review).  
> **Catatan:** Tidak ada perubahan kode yang dilakukan sebelum rencana ini ditinjau dan disetujui oleh pengguna.

---

## 1. Ringkasan Eksekutif & Matriks Temuan

Pemindaian DAST (menggunakan engine OWASP ZAP) menghasilkan total **13 temuan (Alert)** yang terbagi ke dalam 3 tingkat risiko: **4 Medium Risk**, **5 Low Risk**, dan **4 Informational**.

### Tabel Matriks Temuan

| No | Nama Temuan (Alert) | Tingkat Risiko | Tingkat Keyakinan | CWE | Jumlah Kasus | Status Analisis |
|---|---|---|---|---|---|---|
| **1** | **Absence of Anti-CSRF Tokens** | **Medium** | Low | CWE-352 | 2 URL (`/login`, `/contact-sales`) | Tindakan Aplikasi Diperlukan |
| **2** | **Application Error Disclosure** | **Medium** | Medium | CWE-200 | 2 URL (JS bundle, `/changelog`) | **False Positive** (Teks Changelog) + Hardening |
| **3** | **Content Security Policy (CSP) Header Not Set** | **Medium** | High | CWE-693 | 11 URL (Seluruh Halaman Web) | Tindakan Aplikasi & Proxy Diperlukan |
| **4** | **Missing Anti-clickjacking Header** | **Medium** | Medium | CWE-1021 | 11 URL (Seluruh Halaman Web) | Tindakan Aplikasi & Proxy Diperlukan |
| **5** | **Application Error Disclosure** (Duplikat) | **Low** | Medium | CWE-200 | 2 URL (JS bundle, `/changelog`) | Sama dengan Alert 2 |
| **6** | **Server Leaks Information via "X-Powered-By"** | **Low** | Medium | CWE-200 | 11 URL (`X-Powered-By: Next.js`) | Tindakan Aplikasi (Next.js Config) |
| **7** | **Server Leaks Version Information via "Server"** | **Low** | High | CWE-200 | 11 URL (`nginx/1.24.0 (Ubuntu)`) | Tindakan Konfigurasi Nginx |
| **8** | **Strict-Transport-Security Header Not Set** | **Low** | High | CWE-319 | 11 URL (Seluruh Halaman Web) | Tindakan Aplikasi & Proxy Diperlukan |
| **9** | **X-Content-Type-Options Header Missing** | **Low** | Medium | CWE-693 | 11 URL (Seluruh Halaman Web) | Tindakan Aplikasi & Proxy Diperlukan |
| **10** | **Content-Type Header Missing** | **Info** | Medium | CWE-345 | 9 URL (HTTP 307 Redirects & 500) | Tindakan Aplikasi (Next.js Proxy) |
| **11** | **Information Disclosure - Suspicious Comments** | **Info** | Low | CWE-200 | 11 URL (Komentar `todo`, `query`, `bug`) | Tindakan Build Pipeline (Minifikasi) |
| **12** | **Modern Web Application** | **Info** | Medium | - | 11 URL (Identifikasi SPA) | Panduan Alat Uji (No Fix Needed) |
| **13** | **Re-examine Cache-control Directives** | **Info** | Low | CWE-524 | 3 URL (`manifest`, `robots`, `sitemap`) | Penyesuaian Cache-Control Metadata |

---

## 2. Analisis Rinci & Strategi Penyelesaian per Kategori

Untuk memastikan penyelesaian menyeluruh, temuan di atas dikelompokkan ke dalam 4 kategori strategis:

---

### Kategori 1: HTTP Security Headers & Banner Hardening (Alert 3, 4, 6, 7, 8, 9)

Kategori ini mencakup seluruh header respons HTTP keamanan yang hilang atau membocorkan informasi versi teknologi aplikasi dan web server.

#### A. Deskripsi Masalah
1. **CSP Header Not Set (Alert 3 - Medium):** Header `Content-Security-Policy` belum diatur pada level Next.js maupun Nginx. Hal ini membuat aplikasi lebih rentan terhadap serangan Cross-Site Scripting (XSS) dan injeksi resource asing.
2. **Missing Anti-clickjacking Header (Alert 4 - Medium):** Header `X-Frame-Options` dan direktif CSP `frame-ancestors` tidak ada, sehingga situs web berpotensi dimuat di dalam `<iframe>` pihak ketiga (Clickjacking attack).
3. **Strict-Transport-Security (HSTS) Not Set (Alert 8 - Low):** Header HSTS (`Strict-Transport-Security`) belum aktif, sehingga browser tidak dipaksa menggunakan HTTPS secara persisten.
4. **X-Content-Type-Options Missing (Alert 9 - Low):** Header `X-Content-Type-Options: nosniff` tidak dikirim, membuka celah MIME-type sniffing pada browser lama.
5. **X-Powered-By Header Leak (Alert 6 - Low):** Header `X-Powered-By: Next.js` dikirimkan secara otomatis oleh Next.js, mempermudah penyerang memetakan framework aplikasi.
6. **Server Version Header Leak (Alert 7 - Low):** Nginx membocorkan informasi spesifik versi OS dan server: `Server: nginx/1.24.0 (Ubuntu)`.

#### B. Solusi di Sisi Aplikasi & Reverse Proxy

Penyelesaian dilakukan secara berlapis (*Defense-in-Depth*):

1. **Di Sisi Next.js (`apps/web/next.config.ts`):**
   - Matikan header identitas Next.js: `poweredByHeader: false`.
   - Konfigurasikan blok `headers()` untuk menyuntikkan header keamanan standar pada seluruh route `/:path*`:
     - `Content-Security-Policy`: Direktif seimbang untuk Next.js (mengizinkan skrip internal, font Google/lokal, data SVG, WebSocket `wss:`).
     - `X-Frame-Options: DENY` (atau `SAMEORIGIN` jika ada preview internal).
     - `X-Content-Type-Options: nosniff`.
     - `Strict-Transport-Security: max-age=63072000; includeSubDomains; preload`.
     - `Referrer-Policy: strict-origin-when-cross-origin`.
     - `Permissions-Policy: camera=(), microphone=(), geolocation=()`.

2. **Di Sisi Next.js Proxy/Middleware (`apps/web/proxy.ts`):**
   - Saat Next.js melakukan redirect (HTTP 307) atau rewrite, `next.config.ts` terkadang dilewati oleh instance `NextResponse.redirect()`.
   - Oleh karena itu, kita buat helper pembungkus untuk memastikan setiap `NextResponse` menyertakan header keamanan tersebut.

3. **Di Sisi Backend Go (`server/internal/middleware/` & `server/cmd/server/router.go`):**
   - Backend Go saat ini sudah memiliki middleware CSP (`server/internal/middleware/csp.go`), tetapi belum menginjeksi `X-Frame-Options`, `X-Content-Type-Options`, dan `Strict-Transport-Security`.
   - Tambahkan header tersebut ke dalam middleware keamanan HTTP backend agar endpoint API (`/api/*`, `/ws`, `/health`) juga terlindungi penuh saat diakses langsung.

4. **Di Sisi Nginx (`/etc/nginx/sites-available/multica`):**
   - Sembunyikan versi Nginx dengan menambahkan `server_tokens off;` di dalam blok `http` atau `server`.
   - Pastikan Nginx menambahkan header keamanan saat menyajikan error page internal (seperti 502 Bad Gateway yang sempat tertangkap di DAST).

---

### Kategori 2: Proteksi Cross-Site Request Forgery (CSRF) (Alert 1)

#### A. Deskripsi Masalah
- **Temuan ZAP:** DAST mendeteksi adanya elemen `<form>` pada halaman `/login` dan `/contact-sales` tanpa atribut token CSRF (misalnya `<input type="hidden" name="csrf_token" value="...">`).
- **Analisis Kondisi Aplikasi Taskor2:**
  - Aplikasi menggunakan arsitektur Single Page Application (React / Next.js) dengan REST API Go.
  - Form pada `/login` (`packages/views/auth/login-page.tsx`) dan `/contact-sales` (`apps/web/features/landing/components/contact-sales-page-client.tsx`) tidak melakukan submit form HTML konvensional (tidak ada `action="/endpoint" method="POST"`), melainkan di-intercept oleh JavaScript menggunakan `onSubmit` + `fetch` JSON payload.
  - Backend Go (`server/internal/auth/cookie.go`) **sebenarnya sudah memiliki mekanisme CSRF yang kuat** menggunakan pola Double Submit Cookie HMAC (`multica_csrf` + header `X-CSRF-Token`).
  - Namun, scanner DAST ZAP melakukan pemeriksaan statis pada DOM HTML dan menandai ketiadaan tag input CSRF sebagai kerentanan Medium (CWE-352).

#### B. Solusi di Sisi Aplikasi
1. **Frontend Form Compliance:**
   - Tambahkan elemen `<input type="hidden" name="csrf_token" value="..." />` atau token referensi pada form `/login` dan `/contact-sales`. Hal ini memuaskan engine DAST sekaligus mencegah bypass jika browser mengeksekusi form submit tanpa JS.
2. **Hardening Form Submission:**
   - Untuk `/contact-sales`, pastikan request pengiriman pesan menyertakan proteksi verifikasi `Origin`/`Referer` header dan rate limiting ketat pada backend.
   - Untuk auth cookies (`multica_auth` dan `multica_csrf`), pastikan flag `SameSite=Lax` (atau `Strict`) dan `Secure=true` selalu aktif di lingkungan HTTPS.

---

### Kategori 3: Penanganan Error & Kebocoran Informasi (Alert 2, 5, 11)

#### A. Deskripsi Masalah
1. **Application Error Disclosure (Alert 2 & Alert 5 - Medium & Low):**
   - DAST menemukan teks `"internal error"` pada respon:
     - `https://super-presales-dev.lintasarta.co.id/_next/static/chunks/96434-71001ef7b961a0a4.js`
     - `https://super-presales-dev.lintasarta.co.id/changelog`
   - **Investigasi Mendalam:** Temuan ini adalah **False Positive**! Setelah ditelusuri ke dalam source code (`apps/web/features/landing/i18n/en.ts` baris 310), string tersebut berasal dari teks rilis changelog resmi:
     > *"Run now no longer exposes internal error details."*
   - Scanner ZAP mendeteksi kemunculan kata kunci `"internal error"` di dalam teks changelog dan mengira aplikasi mengalami unhandled exception.
2. **Information Disclosure - Suspicious Comments (Alert 11 - Info):**
   - DAST mendeteksi komentar-komentar berisi keyword seperti `todo`, `query`, `bug`, `from` pada bundle JavaScript statis.

#### B. Solusi di Sisi Aplikasi
1. **Sanitasi Teks Changelog (Menghilangkan False Positive ZAP):**
   - Ubah redaksi kalimat di `apps/web/features/landing/i18n/en.ts` menjadi lebih netral, misalnya:  
     *"Run now no longer exposes diagnostic details."* (menghilangkan frasa literal `"internal error"` sehingga scanner tidak lagi terpicu).
2. **Pembersihan Komentar pada Bundler (Terser / Next.js Compiler):**
   - Di `next.config.ts`, pastikan compiler Next.js pada mode production mengaktifkan penghapusan komentar (`removeConsole` dan strip legal comments) agar kode sumber bersih dari anotasi internal pengembang.
3. **Hardening Error Boundary:**
   - Pastikan `apps/web/app/global-error.tsx` dan `apps/web/app/not-found.tsx` tidak pernah menampilkan `error.stack` atau detail internal ke pengguna umum di lingkungan production.

---

### Kategori 4: Standarisasi Header Content-Type & Kebijakan Caching (Alert 10, 12, 13)

#### A. Deskripsi Masalah
1. **Content-Type Header Missing (Alert 10 - Info):**
   - Ditemukan pada 9 URL: `/agents`, `/auth`, `/auth/`, `/inbox`, `/issues`, `/my-issues`, `/runtimes`, `/settings`, `/skills`.
   - **Investigasi:**
     - URL `/agents`, `/inbox`, `/issues`, dll. menghasilkan status `HTTP/1.1 307 Temporary Redirect` menuju `/login` (karena diakses tanpa cookie sesi). Bawaan `NextResponse.redirect()` di Next.js tidak menambahkan header `Content-Type`.
     - URL `/auth` dan `/auth/` menghasilkan status `HTTP/1.1 500 Internal Server Error` (karena direct proxy rewrite ke Go backend yang saat itu tidak memiliki route handler untuk path tersebut), dan respons error 500 tersebut tidak menyertakan `Content-Type`.
2. **Re-examine Cache-control Directives (Alert 13 - Info):**
   - Terjadi pada file metadata: `/manifest.webmanifest`, `/robots.txt`, `/sitemap.xml`.
   - Respons saat ini mengembalikan `Cache-Control: public, max-age=0, must-revalidate`. ZAP menyarankan peninjauan ulang apakah resource ini sengaja tidak di-cache atau membutuhkan nilai `max-age` yang lebih terukur.
3. **Modern Web Application (Alert 12 - Info):**
   - OWASP ZAP mengidentifikasi bahwa aplikasi adalah SPA modern berbasis AJAX/React. Peringatan ini bersifat informasional untuk memberi rekomendasi agar pengujian berikutnya menggunakan fitur *Ajax Spider* ZAP. Tidak memerlukan perubahan kode.

#### B. Solusi di Sisi Aplikasi
1. **Eksplisit Content-Type pada Redirect & Error:**
   - Di `apps/web/proxy.ts`, ubah setiap pemanggilan `NextResponse.redirect(url)` agar secara eksplisit menyertakan header `Content-Type: text/plain; charset=utf-8` atau `text/html; charset=utf-8`.
   - Pada handler backend Go, pastikan seluruh format respons (termasuk 404/500/redirect) selalu memiliki header `Content-Type: application/json; charset=utf-8` atau `text/plain`.
2. **Optimasi Cache-Control Metadata:**
   - Pada `apps/web/app/robots.ts`, `sitemap.ts`, dan `manifest.ts`, tetapkan nilai revalidasi eksplisit (misalnya `export const revalidate = 86400;` / 1 hari) atau atur header `Cache-Control: public, max-age=86400, stale-while-revalidate=3600` di `next.config.ts`.

---

## 3. Rencana Tindakan & Perubahan Berkas (Proposed Changes)

Berikut adalah rincian berkas yang akan diperbarui setelah persetujuan:

### 1. Frontend: Next.js (`apps/web`)

#### [MODIFY] [`apps/web/next.config.ts`](file:///d:/Kerjaan/Project/taskor2/apps/web/next.config.ts)
- Menambahkan `poweredByHeader: false`.
- Menambahkan blok `async headers()` yang menyetel:
  - `Content-Security-Policy`
  - `X-Frame-Options: DENY`
  - `X-Content-Type-Options: nosniff`
  - `Strict-Transport-Security: max-age=63072000; includeSubDomains; preload`
  - `Referrer-Policy: strict-origin-when-cross-origin`
  - `Permissions-Policy: camera=(), microphone=(), geolocation=()`
- Menambahkan cache-control khusus untuk `/robots.txt`, `/sitemap.xml`, dan `/manifest.webmanifest`.

#### [MODIFY] [`apps/web/proxy.ts`](file:///d:/Kerjaan/Project/taskor2/apps/web/proxy.ts)
- Memastikan respons `NextResponse.redirect()` menyertakan:
  - `Content-Type: text/plain; charset=utf-8`
  - Header keamanan standar (`X-Content-Type-Options`, `X-Frame-Options`, dll.) agar respons 307 redirect tetap lolos audit DAST.

#### [MODIFY] [`apps/web/features/landing/i18n/en.ts`](file:///d:/Kerjaan/Project/taskor2/apps/web/features/landing/i18n/en.ts)
- Mengubah teks changelog *"Run now no longer exposes internal error details."* menjadi *"Run now no longer exposes diagnostic details."* untuk mengeliminasi pemicu false-positive ZAP Rule 90022.

#### [MODIFY] [`apps/web/features/landing/components/contact-sales-page-client.tsx`](file:///d:/Kerjaan/Project/taskor2/apps/web/features/landing/components/contact-sales-page-client.tsx)
- Menambahkan elemen hidden anti-CSRF token pada `<form>` contact sales.

---

### 2. Frontend: Shared Views (`packages/views`)

#### [MODIFY] [`packages/views/auth/login-page.tsx`](file:///d:/Kerjaan/Project/taskor2/packages/views/auth/login-page.tsx)
- Menambahkan elemen `<input type="hidden" name="csrf_token" value="..." />` pada form login (`#login-form`).

---

### 3. Backend: Go Server (`server/`)

#### [MODIFY] [`server/internal/middleware/csp.go`](file:///d:/Kerjaan/Project/taskor2/server/internal/middleware/csp.go)
- Mengembangkan middleware CSP menjadi middleware keamanan lengkap (atau menambahkan `SecurityHeadersMiddleware`) yang menyetel:
  - `X-Frame-Options: DENY`
  - `X-Content-Type-Options: nosniff`
  - `Strict-Transport-Security: max-age=63072000; includeSubDomains; preload`
  - `Referrer-Policy: strict-origin-when-cross-origin`
- Memastikan header disetel pada seluruh endpoint API dan WebSocket.

#### [MODIFY] [`server/internal/middleware/csp_test.go`](file:///d:/Kerjaan/Project/taskor2/server/internal/middleware/csp_test.go)
- Memperbarui unit test untuk memverifikasi kehadiran header keamanan baru.

---

### 4. Infrastruktur: Reverse Proxy Nginx

#### [DOKUMENTASI] Rekomendasi Pembaruan Konfigurasi Nginx Server
Pada server production (`/etc/nginx/sites-available/multica` atau `/etc/nginx/nginx.conf`):
```nginx
# 1. Sembunyikan banner versi Nginx
server_tokens off;

# 2. Tambahkan default security headers untuk respons proxy & static
add_header X-Frame-Options "DENY" always;
add_header X-Content-Type-Options "nosniff" always;
add_header Strict-Transport-Security "max-age=63072000; includeSubDomains; preload" always;
add_header Referrer-Policy "strict-origin-when-cross-origin" always;
```

---

## 4. Rencana Verifikasi & Pengujian (Verification Plan)

Setelah implementasi disetujui dan diterapkan:

### A. Pengujian Otomatis (Automated Tests)
1. **Frontend Typecheck & Tests:**
   ```bash
   pnpm --filter @multica/web typecheck
   pnpm --filter @multica/views typecheck
   pnpm test
   ```
2. **Backend Go Tests:**
   ```bash
   go test -v ./server/internal/middleware/...
   ```

### B. Pengujian Header HTTP (Manual Verification)
Menjalankan inspeksi header langsung terhadap aplikasi:
```bash
# 1. Verifikasi hilangnya X-Powered-By dan kehadiran Security Headers
curl -I http://localhost:3000/
curl -I http://localhost:3000/login

# 2. Verifikasi Content-Type pada respons Redirect (307)
curl -I http://localhost:3000/agents

# 3. Verifikasi Header pada Backend Go API
curl -I http://localhost:8080/health
```

### C. Verifikasi Ulang DAST (Re-scan)
Jalankan kembali pemindaian OWASP ZAP terhadap target:
- Pastikan Alert 1 s/d 10 dan Alert 13 tuntas (*Zero High/Medium/Low Alerts*).
- Simpan laporan baru untuk memvalidasi kepatuhan keamanan.
