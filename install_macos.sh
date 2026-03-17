#!/bin/bash

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")" && pwd)"
WHISPER_BUILD_DIR="${PROJECT_ROOT}/whisper.cpp"
RUNTIME_DIR="${PROJECT_ROOT}/whisper"
INSTALL_VENV_DIR="${PROJECT_ROOT}/.install_venv"

cd "${PROJECT_ROOT}"

cleanup_install_env() {
  set +e
  if [[ -n "${VIRTUAL_ENV:-}" ]]; then
    deactivate >/dev/null 2>&1 || true
  fi
  rm -rf "${INSTALL_VENV_DIR}" 2>/dev/null || true
  rm -rf "${WHISPER_BUILD_DIR}" 2>/dev/null || true

  # Fallback for rare macOS cases where rm leaves directories behind.
  if [[ -d "${INSTALL_VENV_DIR}" ]]; then
    python3 - <<'PY' "${INSTALL_VENV_DIR}"
import shutil
import sys

shutil.rmtree(sys.argv[1], ignore_errors=True)
PY
  fi

  if [[ -d "${WHISPER_BUILD_DIR}" ]]; then
    python3 - <<'PY' "${WHISPER_BUILD_DIR}"
import shutil
import sys

shutil.rmtree(sys.argv[1], ignore_errors=True)
PY
  fi
}

setup_install_env() {
  echo "======Creating temporary venv for installation...======"

  if ! command -v python3 >/dev/null 2>&1; then
    echo "[ERROR] python3 not found."
    exit 1
  fi

  rm -rf "${INSTALL_VENV_DIR}"
  python3 -m venv "${INSTALL_VENV_DIR}"

  # shellcheck disable=SC1091
  source "${INSTALL_VENV_DIR}/bin/activate"

  python -m pip install --upgrade pip certifi
  echo "[OK] Temporary venv activated: ${INSTALL_VENV_DIR}"
}

configure_ssl_environment() {
  echo "======Configuring SSL trust for model download...======"

  if [[ -n "${WHISPER_CA_BUNDLE:-}" ]]; then
    if [[ ! -f "${WHISPER_CA_BUNDLE}" ]]; then
      echo "[ERROR] WHISPER_CA_BUNDLE is set but file does not exist: ${WHISPER_CA_BUNDLE}"
      exit 1
    fi

    export SSL_CERT_FILE="${WHISPER_CA_BUNDLE}"
    export REQUESTS_CA_BUNDLE="${WHISPER_CA_BUNDLE}"
    export CURL_CA_BUNDLE="${WHISPER_CA_BUNDLE}"
    export PIP_CERT="${WHISPER_CA_BUNDLE}"
    echo "[OK] Using custom CA bundle from WHISPER_CA_BUNDLE"
    return
  fi

  local certifi_cafile
  certifi_cafile="$(python -c 'import certifi; print(certifi.where())')"
  export SSL_CERT_FILE="${certifi_cafile}"
  export REQUESTS_CA_BUNDLE="${certifi_cafile}"
  export CURL_CA_BUNDLE="${certifi_cafile}"
  export PIP_CERT="${certifi_cafile}"

  if ! python - <<'PY'
import socket
import ssl

HOST = "openaipublic.azureedge.net"
PORT = 443

ctx = ssl.create_default_context()
with socket.create_connection((HOST, PORT), timeout=10) as sock:
    with ctx.wrap_socket(sock, server_hostname=HOST):
        pass
PY
  then
    echo "[ERROR] TLS certificate verification failed while connecting to model host."
    echo "        If your network uses TLS inspection, export WHISPER_CA_BUNDLE=/path/to/your/company-ca.pem"
    echo "        then rerun this script."
    echo "        Temporary (unsafe) fallback only if unavoidable:"
    echo "        export PYTHONHTTPSVERIFY=0"
    exit 1
  fi

  echo "[OK] SSL trust check passed."
}

check_coreml_environment() {
  echo "======Checking Xcode/CoreML environment...======"

  if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "[ERROR] CoreML build is supported only on macOS."
    exit 1
  fi

  if ! command -v xcode-select >/dev/null 2>&1; then
    echo "[ERROR] xcode-select not found. Please install Xcode and Command Line Tools."
    exit 1
  fi

  if ! xcode-select -p >/dev/null 2>&1; then
    echo "[ERROR] Xcode path is not configured."
    echo "        Run: sudo xcode-select --switch /Applications/Xcode.app/Contents/Developer"
    exit 1
  fi

  if ! command -v xcrun >/dev/null 2>&1; then
    echo "[ERROR] xcrun not found. Please install Command Line Tools."
    exit 1
  fi

  if ! xcrun --find coremlc >/dev/null 2>&1; then
    echo "[ERROR] coremlc not found via xcrun."
    echo "        This usually means full Xcode is missing or selected developer path is incorrect."
    echo "        Check Xcode install and developer path:"
    echo "        1) xcode-select -p"
    echo "        2) sudo xcode-select --switch /Applications/Xcode.app/Contents/Developer"
    exit 1
  fi

  echo "[OK] Xcode/CoreML toolchain detected."
}

check_coreml_environment
trap cleanup_install_env EXIT
setup_install_env
configure_ssl_environment

echo "======Installing Python requirements for macOS/CoreML build...======"
python -m pip install -r requirements.txt

echo "======Cloning whisper.cpp repository...======"
rm -rf "${WHISPER_BUILD_DIR}"
git clone https://github.com/ggerganov/whisper.cpp.git

cd "${WHISPER_BUILD_DIR}"

echo "======Configuring CMake for CoreML...======"
cmake -B build -DWHISPER_COREML=1

echo "======Building with CMake...======"
cmake --build build --config Release

echo "======Downloading GGML model (large)...======"
bash ./models/download-ggml-model.sh large-v3

echo "======Generating CoreML model (large)...======"
bash ./models/generate-coreml-model.sh large-v3

echo "======Downloading VAD model (silero)...======"
bash ./models/download-vad-model.sh silero-v6.2.0

echo "======Preparing runtime files in ./whisper...======"
rm -rf "${RUNTIME_DIR}"
mkdir -p "${RUNTIME_DIR}/bin" "${RUNTIME_DIR}/lib" "${RUNTIME_DIR}/models"

cp -f build/bin/whisper-cli "${RUNTIME_DIR}/bin/"
cp -a build/src/libwhisper*.dylib "${RUNTIME_DIR}/lib/"
cp -a build/ggml/src/libggml*.dylib "${RUNTIME_DIR}/lib/"
cp -a build/ggml/src/ggml-blas/libggml-blas*.dylib "${RUNTIME_DIR}/lib/"
cp -a build/ggml/src/ggml-metal/libggml-metal*.dylib "${RUNTIME_DIR}/lib/"

cp -f models/ggml-large-v3.bin "${RUNTIME_DIR}/models/"
cp -f models/ggml-silero-v6.2.0.bin "${RUNTIME_DIR}/models/"
cp -a models/ggml-large-v3-encoder.mlmodelc "${RUNTIME_DIR}/models/"

# Ensure whisper-cli and dylibs can resolve each other after whisper.cpp removal.
if ! otool -l "${RUNTIME_DIR}/bin/whisper-cli" | grep -Fq "@executable_path/../lib"; then
  install_name_tool -add_rpath "@executable_path/../lib" "${RUNTIME_DIR}/bin/whisper-cli"
fi

for lib in "${RUNTIME_DIR}/lib/"*.dylib; do
  if ! otool -l "$lib" | grep -Fq "@loader_path"; then
    install_name_tool -add_rpath "@loader_path" "$lib"
  fi
done

cd "${PROJECT_ROOT}"

echo "======Removing build workspace ./whisper.cpp...======"
rm -rf "${WHISPER_BUILD_DIR}"

echo "======All steps completed.======"

echo "======Testing whisper runtime with a sample audio file...======"

./whisper/bin/whisper-cli \
  -m whisper/models/ggml-large-v3.bin -l ko \
  --vad \
  --vad-model whisper/models/ggml-silero-v6.2.0.bin \
  --suppress-nst \
  --vad-threshold 0.1 \
  --vad-min-speech-duration-ms 500 \
  --vad-min-silence-duration-ms 500 \
  --vad-max-speech-duration-s 15 \
  --vad-speech-pad-ms 150 \
  --vad-samples-overlap 0.05 \
  --no-speech-thold 0.1 \
  --temperature 0.0 \
  --temperature-inc 0.2 \
  --logprob-thold -0.8 \
  --max-context  0 \
  --prompt "소프트웨어공학과 강의" \
  --output-txt sample/test_stt.wav
