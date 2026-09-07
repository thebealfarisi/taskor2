#!/bin/bash
# Setup script for Security Query Analyzer virtual environment

set -e

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd "$SCRIPT_DIR"

echo "=== Setting up Virtual Environment for Security Query Analyzer ==="

# Check Python3 availability
if ! command -v python3 &> /dev/null; then
    echo "[ERROR] python3 is not installed or not in PATH."
    exit 1
fi

# Create .venv if it does not exist
if [ ! -d ".venv" ]; then
    echo "[INFO] Creating virtual environment (.venv)..."
    python3 -m venv .venv
else
    echo "[INFO] Virtual environment (.venv) already exists."
fi

# Activate virtual environment
echo "[INFO] Activating virtual environment..."
source .venv/bin/activate

# Upgrade pip and install requirements
echo "[INFO] Installing dependencies from requirements.txt..."
pip install --upgrade pip
pip install -r requirements.txt

# Ensure permissions on run.sh and analyzer.py
chmod +x analyzer.py || true
chmod +x run.sh || true

echo "=== Setup Completed Successfully! ==="
echo "You can now run the analyzer using: ./run.sh"
