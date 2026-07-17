# Panduan Kustomisasi UI dan Rebranding (Task-Or)

Dokumen ini berisi panduan lengkap untuk melakukan kustomisasi antarmuka pengguna (UI) dan *rebranding* pada repository ini (mengubah dari bawaan "Multica" menjadi aplikasi kustom Anda, misalnya "Task-Or").

Kustomisasi dibagi menjadi 3 bagian utama: **Mengubah Warna Tema**, **Mengganti Logo**, dan **Menyesuaikan Teks Rebranding**.

---

## 1. Mengubah Warna Tema (Contoh: Biru Korporat)

Aplikasi ini menggunakan **Tailwind CSS** dengan pendekatan *design system* berbasis CSS Variables menggunakan format warna `oklch`. 

### File Utama yang Perlu Diubah:
1. **`packages/ui/styles/tokens.css`** (Pusat variabel untuk seluruh komponen UI bersama).
2. **`apps/web/app/custom.css`** (Override tema untuk aplikasi web utama).
3. **`apps/docs/app/global.css`** (Override tema untuk situs dokumentasi).

### Panduan Mengubah Warna:
Cari *root variables* (`:root` untuk mode terang dan `.dark` untuk mode gelap) di file-file tersebut, lalu ubah variabel `--primary`, `--primary-foreground`, dan `--ring`.

Gunakan referensi nilai `oklch` berikut untuk tema Biru Korporat:
- **Light mode:** `--primary: oklch(0.55 0.16 250);` (Biru Solid)
- **Dark mode:** `--primary: oklch(0.65 0.16 250);` (Biru Terang agar kontras di latar gelap)

#### Contoh Perubahan pada `:root` (Light Mode)
```css
:root {
  /* Ganti warna asal menjadi Biru Korporat */
  --primary: oklch(0.55 0.16 250);
  --primary-foreground: oklch(0.98 0.01 250); /* Teks di atas warna primer */
  --ring: oklch(0.55 0.16 250);
}
```

#### Contoh Perubahan pada `.dark` (Dark Mode)
```css
.dark {
  --primary: oklch(0.65 0.16 250);
  --primary-foreground: oklch(0.20 0.05 250); /* Teks gelap di atas warna primer terang */
  --ring: oklch(0.65 0.16 250);
}
```

---

## 2. Mengganti Logo Aplikasi

Logo diaplikasikan di dua tempat: komponen UI global dan favicon browser.

### A. Mengganti Komponen Logo UI (`packages/ui/components/common/multica-icon.tsx`)
Komponen `MulticaIcon` digunakan di berbagai *navbar* dan tata letak aplikasi. Untuk menggunakan logo kustom (misal `logo_baru.svg` berupa base64 PNG/SVG), Anda harus me-return tag `<img>` di dalam komponen tersebut:

1. Ekstrak data base64 dari file SVG Anda.
2. Edit `multica-icon.tsx` agar mengembalikan struktur seperti berikut:

```tsx
const LOGO_BASE64 = "data:image/png;base64,...(masukkan string base64 logo Anda)...";

export function MulticaIcon({ className, ...props }) {
  // ... implementasi size dan border bawaan ...
  return (
    <img 
      src={LOGO_BASE64} 
      className={cn("inline-block size-[1em]", className)} 
      alt="Logo Aplikasi" 
      {...props} 
    />
  );
}
```

### B. Mengganti Favicon Browser (`apps/web/public/favicon.svg`)
1. Salin atau timpa file logo SVG baru Anda ke `apps/web/public/favicon.svg`.
2. **Penting (Cache Busting):** Browser sangat agresif dalam men-cache favicon. Buka file `apps/web/app/layout.tsx` dan tambahkan parameter *cache buster* (seperti `?v=2`) pada referensi favicon:
   ```tsx
   icons: {
     icon: [{ url: "/favicon.svg?v=2", type: "image/svg+xml" }],
     shortcut: ["/favicon.svg?v=2"],
   },
   ```
3. Ubah juga pada file `apps/web/app/favicon.ico/route.ts`:
   ```tsx
   return Response.redirect(new URL("/favicon.svg?v=2", request.url), 308);
   ```

---

## 3. Rebranding Teks (Dari "Multica" ke Nama Kustom)

Ganti informasi *user-facing* pada metadata situs, halaman informasi, dan alur aplikasi. Jangan mengubah nama paket (`@multica/core`), nama komponen `<MulticaIcon />`, atau protokol *deep-link* (`multica://`) karena hal itu dapat merusak fungsionalitas sistem. 

Hanya ubah teks statis / metadata pada file-file berikut di dalam `apps/web/app`:
- `(landing)/homepage/page.tsx`
- `(landing)/about/page.tsx`
- `(landing)/changelog/page.tsx`
- `(landing)/contact-sales/page.tsx`
- `(landing)/download/page.tsx`
- `layout.tsx` (Root dan Landing metadata)
- `not-found.tsx`
- `auth/callback/page.tsx` (Pesan untuk membuka Desktop app)

Cukup gunakan fungsi pencarian/Find & Replace teks (huruf besar/kecil sensitif) dan ubah "Multica" menjadi "Task-Or" (atau nama perusahaan Anda).

---

## Tips Tambahan
1. **Converter Warna:** Gunakan [oklch.com](https://oklch.com/) untuk mengubah warna HEX perusahaan Anda ke format `oklch`.
2. **Reload Server:** Jika Next.js *Hot Module Replacement* (HMR) tidak langsung memuat perubahan secara global, hentikan server lalu mulai lagi (`make dev` atau `npm run dev`).
3. **Hard Refresh:** Tekan `Ctrl + F5` (Windows) atau `Cmd + Shift + R` (Mac) di browser Anda untuk memastikan logo favicon dan CSS yang baru di-download secara *fresh*.
