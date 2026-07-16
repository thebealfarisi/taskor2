# Customizing the UI Theme

Multica menggunakan Tailwind CSS dengan pendekatan design system berbasis CSS Variables (biasanya dalam format `oklch` atau `hsl`). Untuk mengubah tema bawaan menjadi tema kustom, seperti "Biru Korporat" (Corporate Blue), Anda perlu memodifikasi file-file CSS utama di repositori ini.

## File Utama yang Perlu Diubah

Tema Multica tersebar di beberapa file tergantung dari aplikasinya (karena ini adalah monorepo). Namun, titik utamanya ada di `packages/ui/styles/tokens.css` dan beberapa file `global.css`/`custom.css` pada setiap aplikasi.

Berikut daftar file yang harus diubah:

1. **`packages/ui/styles/tokens.css`**
   Ini adalah *source of truth* untuk seluruh komponen UI bersama (berbasis shadcn/Base UI).
2. **`apps/web/app/custom.css`** (atau `globals.css`)
   Digunakan untuk override tema di aplikasi web utama (Next.js).
3. **`apps/docs/app/global.css`**
   Digunakan jika Anda juga ingin menyeragamkan warna pada situs dokumentasi.
4. **`apps/mobile/global.css`**
   Digunakan jika Anda mendevelop aplikasi mobile menggunakan Expo/React Native.

## Panduan Mengubah Menjadi "Biru Korporat"

Buka file-file di atas, lalu cari *root variables* (seperti `:root` dan `.dark` atau `@media (prefers-color-scheme: dark)`). Anda akan menemukan variabel `--primary`.

Ubah nilainya menjadi warna biru korporat pilihan Anda. Karena Multica menggunakan `oklch`, Anda bisa menggunakan referensi seperti:
- **Light mode:** `--primary: oklch(0.55 0.16 250);` (Biru Solid)
- **Dark mode:** `--primary: oklch(0.65 0.16 250);` (Biru Terang agar kontras di latar gelap)

### Contoh Perubahan pada `:root`

```css
:root {
  /* ...variabel lainnya... */
  
  /* Ganti dari warna asal ke Biru Korporat */
  --primary: oklch(0.55 0.16 250);
  --primary-foreground: oklch(0.98 0.01 250); /* Teks di atas warna primer (biasanya putih/terang) */
  
  /* Sesuaikan juga warna pendukung bila perlu (ring, border, dll) */
  --ring: oklch(0.55 0.16 250);
}
```

### Contoh Perubahan pada Mode Gelap (`.dark`)

```css
.dark {
  /* ...variabel lainnya... */
  
  --primary: oklch(0.65 0.16 250);
  --primary-foreground: oklch(0.20 0.05 250); /* Teks gelap di atas warna primer terang */
  
  --ring: oklch(0.65 0.16 250);
}
```

## Tips Tambahan
- Anda bisa menggunakan converter warna seperti [oklch.com](https://oklch.com/) untuk mendapatkan nilai kustom dari warna HEX biru perusahaan Anda (misalnya `#0055AA`).
- Pastikan untuk me-restart *development server* (jika sedang berjalan) agar perubahan warna ini dapat di-compile dengan benar oleh Tailwind.
