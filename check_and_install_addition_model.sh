#!/usr/bin/env bash
#
# check_and_install_addition_model.sh
#
# Purpose : Menambahkan konfigurasi endpoint dekaLLM Lintas untuk SATU model
#           BARU pada Claude Code, TANPA menghapus/mengganti konfigurasi
#           model-model yang sebelumnya sudah ditambahkan lewat
#           check_and_install_claude.sh atau script ini sendiri.
#
#           Setiap model disimpan di blok tersendiri di .bashrc (marker unik
#           per alias model) dan diekspos lewat shell function
#           `use-<alias>` untuk mengaktifkan model tersebut kapan pun tanpa
#           menimpa konfigurasi model lain yang sudah terpasang.
#
# Catatan : Skrip ini TIDAK melakukan instalasi Claude Code. Claude Code
#           diasumsikan sudah terinstall dan berjalan sebagai daemon di
#           server ini (sama seperti asumsi check_and_install_claude.sh).
#
# Usage   : bash check_and_install_addition_model.sh
#

set -uo pipefail

# ---------------------------------------------------------------------------
# Konfigurasi tetap
# ---------------------------------------------------------------------------
PROJECT_DIR="/home/alpaca/super-presales-dev/"
BASHRC_FILE="${HOME}/.bashrc"
DEFAULT_DEKA_BASE="https://super-presales-dev.lintasarta.co.id/"

# ---------------------------------------------------------------------------
# Helper log
# ---------------------------------------------------------------------------
log_info()  { echo -e "[INFO]  $1"; }
log_ok()    { echo -e "[OK]    $1"; }
log_warn()  { echo -e "[WARN]  $1"; }
log_error() { echo -e "[ERROR] $1"; }

# Menghapus SEMUA baris di antara dua marker literal (termasuk marker itu
# sendiri) memakai perbandingan baris persis (bukan regex), supaya aman
# terhadap karakter spesial seperti '(' ')' '.' pada teks marker. Juga
# membersihkan duplikat blok jika ada lebih dari satu pasang marker yang sama.
remove_marked_block() {
    local file="$1" start="$2" end="$3" tmp
    tmp="$(mktemp "${file}.XXXXXX")"
    awk -v s="$start" -v e="$end" '
        $0 == s {skip=1; next}
        $0 == e {skip=0; next}
        skip != 1 {print}
    ' "$file" > "$tmp" && mv "$tmp" "$file"
}

echo "======================================================================"
echo " Tambah Model Baru - dekaLLM Lintas (tanpa menghapus model lama)"
echo "======================================================================"

# ---------------------------------------------------------------------------
# TAHAP 1: PENGECEKAN CLI & DAEMON (sama seperti check_and_install_claude.sh)
# ---------------------------------------------------------------------------
log_info "Tahap 1/5: Memeriksa status Claude Code di server ini..."

CLAUDE_EXISTS=false

if command -v claude >/dev/null 2>&1; then
    CLAUDE_EXISTS=true
    log_ok "Perintah 'claude' tersedia: $(command -v claude)"
    CLAUDE_VERSION="$(claude --version 2>/dev/null || echo 'tidak diketahui')"
    log_ok "Versi Claude Code: ${CLAUDE_VERSION}"
else
    log_error "Perintah 'claude' TIDAK ditemukan di PATH."
    log_error "Sesuai asumsi, Claude Code seharusnya sudah terinstall di server ini sebagai daemon."
    log_warn "Periksa apakah PATH shell sudah benar, atau hubungi admin untuk memastikan instalasi Claude Code."
fi

log_info "Memeriksa proses daemon Claude Code yang sedang berjalan..."
if pgrep -f "claude" >/dev/null 2>&1; then
    log_ok "Ditemukan proses 'claude' yang berjalan di server ini:"
    pgrep -fa "claude" 2>/dev/null | sed 's/^/         /'
else
    log_warn "Tidak ditemukan proses 'claude' yang sedang berjalan (daemon mungkin belum start atau berjalan dengan nama proses lain)."
fi

if command -v systemctl >/dev/null 2>&1; then
    if systemctl list-units --type=service --all 2>/dev/null | grep -qi "claude"; then
        log_info "Status service systemd terkait 'claude':"
        systemctl list-units --type=service --all 2>/dev/null | grep -i "claude" | sed 's/^/         /'
    else
        log_info "Tidak ada service systemd bernama 'claude' terdaftar (mungkin dijalankan manual/PM2/nohup)."
    fi
fi

# ---------------------------------------------------------------------------
# TAHAP 2: VALIDASI (tanpa instalasi)
# ---------------------------------------------------------------------------
log_info "Tahap 2/5: Validasi kesiapan runtime..."

if [ "$CLAUDE_EXISTS" = true ]; then
    log_ok "Claude Code siap digunakan. Skrip melanjutkan ke tahap konfigurasi model baru."
else
    log_error "Claude Code belum siap. Skrip tetap melanjutkan ke tahap konfigurasi,"
    log_error "namun 'claude --version' di akhir skrip kemungkinan akan gagal."
    log_warn "Skrip ini tidak melakukan instalasi otomatis. Jika perlu, install manual dengan hak akses root, contoh:"
    log_warn "  sudo npm install -g @anthropic-ai/claude-code"
fi

# ---------------------------------------------------------------------------
# TAHAP 3: INTERAKTIF INPUT MODEL BARU
# ---------------------------------------------------------------------------
log_info "Tahap 3/5: Detail model baru yang akan ditambahkan."
echo "Alias dipakai sebagai identitas unik model ini (nama variabel & nama function switch),"
echo "sehingga model-model lain yang sudah terpasang sebelumnya TIDAK akan terpengaruh."

read -r -p "Masukkan ALIAS model (contoh: llama4maverick, hanya huruf/angka/underscore): " MODEL_ALIAS_RAW
read -r -p "Masukkan INPUT_OPENAI_DEKA_KEY (API Key)               : " INPUT_OPENAI_DEKA_KEY
read -r -p "Masukkan INPUT_OPENAI_DEKA_BASE [default: ${DEFAULT_DEKA_BASE}] : " INPUT_OPENAI_DEKA_BASE
read -r -p "Masukkan INPUT_OPENAI_DEKA_MODEL (Nama Model)         : " INPUT_OPENAI_DEKA_MODEL

INPUT_OPENAI_DEKA_BASE="${INPUT_OPENAI_DEKA_BASE:-$DEFAULT_DEKA_BASE}"

if [ -z "$MODEL_ALIAS_RAW" ] || [ -z "$INPUT_OPENAI_DEKA_KEY" ] || [ -z "$INPUT_OPENAI_DEKA_BASE" ] || [ -z "$INPUT_OPENAI_DEKA_MODEL" ]; then
    log_error "Salah satu input kosong. Semua nilai wajib diisi. Skrip dihentikan."
    exit 1
fi

# Sanitasi alias: hanya huruf/angka/underscore, lowercase, tidak diawali angka
MODEL_ALIAS="$(echo "$MODEL_ALIAS_RAW" | tr -cd '[:alnum:]_' | tr '[:upper:]' '[:lower:]')"
if [ -z "$MODEL_ALIAS" ] || [[ "$MODEL_ALIAS" =~ ^[0-9] ]]; then
    log_error "Alias tidak valid setelah sanitasi ('${MODEL_ALIAS_RAW}' -> '${MODEL_ALIAS}')."
    log_error "Gunakan alias yang diawali huruf, hanya huruf/angka/underscore. Skrip dihentikan."
    exit 1
fi
log_ok "Alias model dinormalisasi menjadi: ${MODEL_ALIAS}"

MODEL_ALIAS_UPPER="$(echo "$MODEL_ALIAS" | tr '[:lower:]' '[:upper:]')"
VAR_KEY="DEKA_${MODEL_ALIAS_UPPER}_KEY"
VAR_BASE="DEKA_${MODEL_ALIAS_UPPER}_BASE"
VAR_MODEL="DEKA_${MODEL_ALIAS_UPPER}_MODEL"
SWITCH_FN="use-${MODEL_ALIAS}"

log_ok "Semua input diterima."

# ---------------------------------------------------------------------------
# Normalisasi & pengujian akhiran '/v1' pada INPUT_OPENAI_DEKA_BASE
# ---------------------------------------------------------------------------
INPUT_OPENAI_DEKA_BASE="${INPUT_OPENAI_DEKA_BASE%/}"
if [[ "$INPUT_OPENAI_DEKA_BASE" != */v1 ]]; then
    INPUT_OPENAI_DEKA_BASE="${INPUT_OPENAI_DEKA_BASE}/v1"
fi

log_info "Menguji konektivitas endpoint: ${INPUT_OPENAI_DEKA_BASE}"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 "$INPUT_OPENAI_DEKA_BASE" 2>/dev/null || echo "000")
log_info "Hasil pengujian koneksi: HTTP ${HTTP_CODE}"

if [ "$HTTP_CODE" = "404" ] || [ "$HTTP_CODE" = "000" ]; then
    log_warn "Endpoint dengan akhiran '/v1' tidak merespons dengan baik (HTTP ${HTTP_CODE})."
    log_warn "Menghapus akhiran '/v1' dan menggunakan base URL asli sebagai fallback."
    INPUT_OPENAI_DEKA_BASE="${INPUT_OPENAI_DEKA_BASE%/v1}"
    log_ok "INPUT_OPENAI_DEKA_BASE final: ${INPUT_OPENAI_DEKA_BASE}"
else
    log_ok "Endpoint '/v1' merespons (HTTP ${HTTP_CODE}). Menggunakan URL ini."
fi

# Export ke environment runtime saat ini (namespaced per-alias, tidak menimpa model lain)
export "${VAR_KEY}=${INPUT_OPENAI_DEKA_KEY}"
export "${VAR_BASE}=${INPUT_OPENAI_DEKA_BASE}"
export "${VAR_MODEL}=${INPUT_OPENAI_DEKA_MODEL}"

log_ok "Variabel environment '${VAR_KEY}', '${VAR_BASE}', '${VAR_MODEL}' berhasil di-export untuk sesi ini."
log_info "Model ini BELUM otomatis aktif sebagai model default Claude Code."
log_info "Gunakan function '${SWITCH_FN}' (tersedia setelah source .bashrc) untuk mengaktifkannya kapan pun,"
log_info "tanpa memengaruhi konfigurasi model lain yang sudah terpasang."

# ---------------------------------------------------------------------------
# TAHAP 4: RUNTIME ENVIRONMENT & PATH PERSISTENCE (tambah, bukan hapus)
# ---------------------------------------------------------------------------
log_info "Tahap 4/5: Menyimpan konfigurasi model baru ke ${BASHRC_FILE}..."

if [ ! -d "$PROJECT_DIR" ]; then
    log_warn "Direktori project '$PROJECT_DIR' tidak ditemukan di server ini."
else
    log_ok "Direktori project ditemukan: $PROJECT_DIR"
fi

# Backup .bashrc sebelum diubah
if [ -f "$BASHRC_FILE" ]; then
    cp "$BASHRC_FILE" "${BASHRC_FILE}.bak.$(date +%Y%m%d%H%M%S)"
    log_ok "Backup ${BASHRC_FILE} dibuat."
fi

MARK_START="# >>> dekaLLM Lintas - model:${MODEL_ALIAS} (managed by check_and_install_addition_model.sh) >>>"
MARK_END="# <<< dekaLLM Lintas - model:${MODEL_ALIAS} <<<"

# Hapus HANYA blok milik alias ini jika sudah pernah ditambahkan sebelumnya
# (memperbarui ulang alias yang sama), blok model lain tidak disentuh sama sekali.
if [ -f "$BASHRC_FILE" ] && grep -qF "$MARK_START" "$BASHRC_FILE"; then
    log_info "Konfigurasi model '${MODEL_ALIAS}' sudah pernah ditambahkan sebelumnya, memperbarui blok tersebut saja..."
    remove_marked_block "$BASHRC_FILE" "$MARK_START" "$MARK_END"
else
    log_info "Belum ada konfigurasi untuk alias '${MODEL_ALIAS}'. Menambahkan blok baru tanpa menyentuh model lain."
fi

{
    echo ""
    echo "$MARK_START"
    echo "export ${VAR_KEY}=\"${INPUT_OPENAI_DEKA_KEY}\""
    echo "export ${VAR_BASE}=\"${INPUT_OPENAI_DEKA_BASE}\""
    echo "export ${VAR_MODEL}=\"${INPUT_OPENAI_DEKA_MODEL}\""
    echo "${SWITCH_FN}() {"
    echo "    export ANTHROPIC_AUTH_TOKEN=\"\${${VAR_KEY}}\""
    echo "    export ANTHROPIC_BASE_URL=\"\${${VAR_BASE}}\""
    echo "    export ANTHROPIC_MODEL=\"\${${VAR_MODEL}}\""
    echo "    echo \"[OK] Model aktif sekarang: ${MODEL_ALIAS} (\${${VAR_MODEL}})\""
    echo "}"
    echo "$MARK_END"
} >> "$BASHRC_FILE"

log_ok "Konfigurasi model '${MODEL_ALIAS}' ditambahkan ke ${BASHRC_FILE} tanpa menghapus blok model lain."
log_warn "Catatan keamanan: API key disimpan dalam bentuk plaintext di ${BASHRC_FILE}."
chmod 644 "$BASHRC_FILE" 2>/dev/null || log_warn "Gagal mengubah permission ${BASHRC_FILE}, silakan cek manual."
log_ok "Permission ${BASHRC_FILE} distandarkan ke 644 (agar tidak menyebabkan gagal login/profile error saat relogin via JumpServer)."

if [ -d "$PROJECT_DIR" ]; then
    cd "$PROJECT_DIR" || log_error "Gagal masuk ke direktori $PROJECT_DIR"
    log_ok "Berhasil masuk ke direktori project: $(pwd)"
else
    log_warn "Melewati 'cd' karena direktori project belum ada."
fi

# ---------------------------------------------------------------------------
# TAHAP 5: RINGKASAN MODEL YANG TERPASANG
# ---------------------------------------------------------------------------
log_info "Tahap 5/5: Daftar model yang tercatat di ${BASHRC_FILE} (termasuk yang lama)..."
if [ -f "$BASHRC_FILE" ]; then
    grep -oE '# >>> dekaLLM Lintas - model:[a-z0-9_]+' "$BASHRC_FILE" 2>/dev/null \
        | sed -E 's/.*model:([a-z0-9_]+)/  - \1 (aktifkan dengan: use-\1)/' \
        | sort -u || true
fi

echo "======================================================================"
log_ok "Model baru '${MODEL_ALIAS}' berhasil ditambahkan tanpa menghapus model sebelumnya."
log_info "Jalankan: source ${BASHRC_FILE}   (atau login ulang) agar function '${SWITCH_FN}' tersedia."
log_info "Aktifkan model ini kapan pun dengan menjalankan: ${SWITCH_FN}"
log_info "Verifikasi setelah aktivasi dengan: claude --version"
echo "======================================================================"
