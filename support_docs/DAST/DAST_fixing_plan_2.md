# Rencana Perbaikan Keamanan DAST Tahap 2 (Berdasarkan DAST-2.html)

Dokumen ini disusun berdasarkan hasil pemindaian ulang DAST yang tercatat pada file [`DAST-2.html`](file:///d:/Kerjaan/Project/taskor2/support_docs/DAST/DAST-2.html) terhadap domain `https://super-presales-dev.lintasarta.co.id/`.

> [!IMPORTANT]
> **Status:** Menunggu Persetujuan (Pending Review).  
> **Prinsip:** Tidak ada perubahan kode yang dilakukan sebelum rencana ini ditinjau dan disetujui oleh pengguna.

---

## 1. Kemajuan Signifikan (Perbandingan DAST-1 vs DAST-2)

Implementasi tahap pertama berhasil **menyelesaikan 7 temuan utama** dari total 13 temuan awal. Ukuran laporan berkurang drastis dari **29.9 MB menjadi 13.7 MB**.

### Temuan yang Berhasil Dituntaskan (RESOLVED):
- [x] **Absence of Anti-CSRF Tokens:** TUNTAS (0 kasus).
- [x] **Application Error Disclosure (Medium & Low):** TUNTAS (0 kasus, false-positive teks changelog berhasil dieliminasi).
- [x] **Missing Anti-clickjacking Header:** TUNTAS (0 kasus, header X-Frame-Options aktif).
- [x] **Server Leaks Information via "X-Powered-By":** TUNTAS (0 kasus, `X-Powered-By: Next.js` berhasil dihapus).
- [x] **Server Leaks Version via "Server":** TUNTAS (0 kasus, `nginx/1.24.0 (Ubuntu)` berhasil disembunyikan menjadi hanya `Server: nginx`).
- [x] **Strict-Transport-Security Header Not Set:** TUNTAS (HSTS sekarang sudah aktif di semua respons).
- [x] **X-Content-Type-Options Header Missing:** TUNTAS (`nosniff` sudah aktif).
- [x] **Content-Type Header Missing:** Berkurang drastis dari 9 kasus menjadi **hanya 1 kasus** (pada trailing slash `/auth/`).

---

## 2. Matriks Temuan Sisa pada DAST-2

Pada pemindaian ulang `DAST-2.html`, tersisa **9 temuan (Alert)**:

| No | Temuan (Alert) | Tingkat Risiko | Jumlah Kasus | Status & Prioritas |
|---|---|---|---|---|
| **1** | **CSP: Wildcard Directive** | **Medium** | 3 URL (`/`, `/about`, `/changelog`) | Perlu Pengetatan Direktif CSP |
| **2** | **CSP: script-src unsafe-inline** | **Medium** | 3 URL (`/`, `/about`, `/changelog`) | Perlu Optimasi CSP (Hapus `unsafe-eval`) |
| **3** | **CSP: style-src unsafe-inline** | **Medium** | 3 URL (`/`, `/about`, `/changelog`) | Trade-off CSS-in-JS Next.js / Hardening |
| **4** | **Strict-Transport-Security Multiple Header Entries** | **Low** | 11 URL (Seluruh Respons) | Perlu Eliminasi Header Duplikat |
| **5** | **Content-Type Header Missing** | **Info** | 1 URL (`/auth/`) | Perlu Trailing Slash Handler di `proxy.ts` |
| **6** | **Sensitive Information in URL (`?csrf_token`)** | **Info** | 2 URL (`/login`, `/contact-sales`) | Tambahkan `method="post"` pada form |
| **7** | **Information Disclosure - Suspicious Comments** | **Info** | 11 URL (Komentar JS bundle) | Optimasi Terser `extractComments: false` |
| **8** | **Modern Web Application** | **Info** | 11 URL (Identifikasi SPA) | Panduan Alat Uji (No Fix Needed) |
| **9** | **Re-examine Cache-control Directives** | **Info** | 4 URL (`/api/config`, metadata) | Cache-Control di Go API & Metadata |

---

## 3. Analisis Mendalam & Strategi Penyelesaian

---

### Kategori 1: Resolusi Header Duplikat (Alert 4 - Low Risk)

#### A. Akar Masalah
Pada respons HTTP saat ini, browser menerima header berikut sebanyak **dua kali**:
```http
Strict-Transport-Security: max-age=63072000; includeSubDomains; preload
...
Strict-Transport-Security: max-age=63072000; includeSubDomains; preload
```
*(Serta duplikasi pada `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, dan `Referrer-Policy`).*

Penyebabnya:
1. Next.js (`apps/web/next.config.ts`) mengirim header HSTS dan security headers.
2. Nginx (`/etc/nginx/sites-available/multica`) juga menambahkan `add_header Strict-Transport-Security ... always;`.
RFC 6797 melarang pengiriman header HSTS ganda dalam satu respons HTTP.

#### B. Solusi
Karena Nginx bertindak sebagai reverse proxy terluar (terminator SSL/TLS) dan sudah menginjeksi header keamanan dengan parameter `always;`, kita **menghapus** injeksi header `Strict-Transport-Security`, `X-Frame-Options`, `X-Content-Type-Options`, dan `Referrer-Policy` dari blok `headers()` di `next.config.ts`.
- **Hasil:** Setiap respons hanya memiliki **tepat 1 header** HSTS dan security headers dari Nginx. Alert 4 tuntas seketika.

---

### Kategori 2: Pengetatan Content Security Policy (CSP) (Alert 1, 2, 3 - Medium Risk)

#### A. Akar Masalah
1. **Wildcard Directive (Alert 1):** Header CSP saat ini memuat skema terbuka tanpa batasan domain:
   - `connect-src 'self' https: wss: ws:`
   - `img-src 'self' data: https: blob:`
   ZAP menandai penggunaan skema wildcard seperti `https:`, `wss:`, `ws:` karena penyerang secara teori dapat mengeksfiltrasi data ke sembarang domain HTTPS/WSS eksternal.
2. **script-src unsafe-inline & unsafe-eval (Alert 2):** Direktif `script-src` memuat `'unsafe-eval'` dan `'unsafe-inline'`. Pada Next.js production build, `'unsafe-eval'` sebenarnya tidak dibutuhkan.
3. **style-src unsafe-inline (Alert 3):** Direktif `style-src` memuat `'unsafe-inline'` yang dipicu oleh styling dinamis Tailwind / CSS custom properties.

#### B. Solusi
Perbarui konfigurasi CSP di `apps/web/next.config.ts`:
1. **Hapus skema wildcard:**
   - Ubah `connect-src 'self' https: wss: ws:` menjadi `connect-src 'self' https://fonts.googleapis.com;` (koneksi WebSocket `/ws` berada pada domain yang sama `'self'`, sehingga tidak memerlukan wildcard `wss:` terbuka).
   - Ubah `img-src 'self' data: https: blob:` menjadi `img-src 'self' data: blob: https://*.lintasarta.co.id;` atau `'self' data: blob:;`.
2. **Hapus `'unsafe-eval'`:**
   - Pada build production, buang `'unsafe-eval'` dari `script-src`.
3. **Perketat direktif CSP final:**
   ```text
   default-src 'self';
   script-src 'self' 'unsafe-inline';
   style-src 'self' 'unsafe-inline' https://fonts.googleapis.com;
   font-src 'self' https://fonts.gstatic.com data:;
   img-src 'self' data: blob: https://*.lintasarta.co.id;
   connect-src 'self';
   frame-ancestors 'none';
   object-src 'none';
   base-uri 'self';
   form-action 'self';
   ```

---

### Kategori 3: Pencegahan Token Masuk ke URL Query String (Alert 6 - Info)

#### A. Akar Masalah
DAST mendeteksi URL:
- `https://super-presales-dev.lintasarta.co.id/contact-sales?csrf_token`
- `https://super-presales-dev.lintasarta.co.id/login?csrf_token`

Mengapa ini terjadi?
Pada tahap 1, kita menambahkan elemen:
`<input type="hidden" name="csrf_token" value="" />` ke dalam `<form id="login-form">` dan form contact sales. Namun, tag `<form>` tersebut **tidak memiliki atribut `method="post"`** (secara default HTML adalah `GET`).
Ketika crawler ZAP menguji submit form tanpa JavaScript, browser/crawler mengeksekusi HTTP `GET`, sehingga seluruh field form (termasuk `csrf_token`) di-append ke URL sebagai query parameter: `?csrf_token=`. ZAP Rule 10043 menandai ini sebagai *Sensitive Information in URL*.

#### B. Solusi
Tambahkan secara eksplisit atribut `method="post"` pada kedua elemen form:
1. `packages/views/auth/login-page.tsx`:
   ```tsx
   <form id="login-form" method="post" onSubmit={handleSendCode} className="space-y-4">
   ```
2. `apps/web/features/landing/components/contact-sales-page-client.tsx`:
   ```tsx
   <form method="post" onSubmit={onSubmit} className="space-y-8 ...">
   ```
Dengan `method="post"`, form submit akan selalu dikirim melalui body HTTP request dan **tidak akan pernah** muncul di URL query string.

---

### Kategori 4: Content-Type pada Trailing Slash Redirect `/auth/` (Alert 5 - Info)

#### A. Akar Masalah
URL `https://super-presales-dev.lintasarta.co.id/auth/` menghasilkan status:
`HTTP/1.1 308 Permanent Redirect` menuju `/auth` tanpa header `Content-Type`.
Ini adalah mekanisme bawaan Next.js trailing slash redirect (`trailingSlash: false`) yang tidak menyertakan header `Content-Type`.

#### B. Solusi
Di dalam `apps/web/proxy.ts` (Next.js middleware), tambahkan penanganan awal untuk me-normalisasi trailing slash:
```ts
if (pathname.length > 1 && pathname.endsWith("/")) {
  const cleanUrl = req.nextUrl.clone();
  cleanUrl.pathname = pathname.replace(/\/+$/, "");
  return redirectWithSecurityHeaders(cleanUrl, 308);
}
```
Karena fungsi `redirectWithSecurityHeaders()` selalu menyertakan `Content-Type: text/plain; charset=utf-8`, respons 308 ini akan memiliki `Content-Type` yang valid, sehingga Alert 5 tuntas.

---

### Kategori 5: Kebijakan Caching pada API & Metadata (Alert 9 - Info)

#### A. Akar Masalah
- `/api/config` tidak memiliki header `Cache-Control`.
- Metadata `/manifest.webmanifest`, `/robots.txt`, `/sitemap.xml` sebelumnya memakai `max-age=86400, stale-while-revalidate=3600`. ZAP Rule 10015 mensyaratkan direktif `immutable` untuk static asset atau `no-store` untuk dynamic API.

#### B. Solusi
1. **Di Go Backend (`server/cmd/server/router.go`):**
   Tambahkan header anti-caching pada middleware global API:
   ```go
   w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
   w.Header().Set("Pragma", "no-cache")
   ```
2. **Di Next.js (`apps/web/next.config.ts`):**
   Ubah header metadata menjadi:
   `Cache-Control: public, max-age=86400, immutable`

---

### Kategori 6: Sanitasi Komentar Webpack Terser (Alert 7 - Info)

#### A. Akar Masalah
Beberapa chunk JS masih memuat teks kata kunci seperti `todo`, `query`, `bug`.
#### B. Solusi
Di `apps/web/next.config.ts`, perkuat konfigurasi Webpack Terser:
```ts
minimizer.options.extractComments = false;
minimizer.options.terserOptions.format.comments = false;
```

---

## 4. Berkas yang Akan Diubah (Proposed Changes)

1. **[`apps/web/next.config.ts`](file:///d:/Kerjaan/Project/taskor2/apps/web/next.config.ts):**
   - Hapus header yang terduplikasi dengan Nginx (`Strict-Transport-Security`, `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy`).
   - Perketat CSP (hapus `'unsafe-eval'` dan wildcard schemes `https:`, `wss:`, `ws:`).
   - Set cache metadata dengan `immutable`.
   - Set `extractComments: false`.

2. **[`apps/web/proxy.ts`](file:///d:/Kerjaan/Project/taskor2/apps/web/proxy.ts):**
   - Tambahkan trailing slash interceptor dengan status 308 + `Content-Type: text/plain; charset=utf-8`.

3. **[`packages/views/auth/login-page.tsx`](file:///d:/Kerjaan/Project/taskor2/packages/views/auth/login-page.tsx):**
   - Tambahkan atribut `method="post"` pada elemen `<form id="login-form">`.

4. **[`apps/web/features/landing/components/contact-sales-page-client.tsx`](file:///d:/Kerjaan/Project/taskor2/apps/web/features/landing/components/contact-sales-page-client.tsx):**
   - Tambahkan atribut `method="post"` pada elemen `<form>`.

5. **[`server/cmd/server/router.go`](file:///d:/Kerjaan/Project/taskor2/server/cmd/server/router.go):**
   - Tambahkan header `Cache-Control: no-store, no-cache, must-revalidate` pada API endpoints.

---

## 5. Rencana Verifikasi (Verification Plan)

1. **Automated Tests:**
   - `pnpm --filter @multica/web test proxy.test.ts`
   - `pnpm --filter @multica/web typecheck`
   - `pnpm --filter @multica/views typecheck`
   - `go test -v ./server/internal/middleware/...`
2. **Curl Header Inspection:**
   - Verifikasi hanya ada **satu** header `Strict-Transport-Security`.
   - Verifikasi `/auth/` mengembalikan 308 dengan `Content-Type: text/plain`.
   - Verifikasi CSP tidak lagi mengandung wildcard scheme atau `unsafe-eval`.
