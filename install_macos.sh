#!/bin/bash

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")" && pwd)"
WHISPER_BUILD_DIR="${PROJECT_ROOT}/whisper.cpp"
RUNTIME_DIR="${PROJECT_ROOT}/whisper"
INSTALL_VENV_DIR="${PROJECT_ROOT}/.install_venv"
DEFAULT_MODEL_NAME="large-v3"
VAD_MODEL_NAME="silero-v6.2.0"
AVAILABLE_MODELS=(
  "tiny|Fastest, lowest accuracy"
  "tiny.en|English-only, fastest"
  "tiny-q5_1|Quantized tiny model"
  "tiny.en-q5_1|English-only quantized tiny model"
  "tiny-q8_0|Higher-quality quantized tiny model"
  "base|Balanced entry model"
  "base.en|English-only base model"
  "base-q5_1|Quantized base model"
  "base.en-q5_1|English-only quantized base model"
  "base-q8_0|Higher-quality quantized base model"
  "small|Better accuracy than base"
  "small.en|English-only small model"
  "small-q5_1|Quantized small model"
  "small.en-q5_1|English-only quantized small model"
  "small-q8_0|Higher-quality quantized small model"
  "medium|Higher accuracy, heavier runtime"
  "medium.en|English-only medium model"
  "medium-q5_0|Quantized medium model"
  "medium.en-q5_0|English-only quantized medium model"
  "medium-q8_0|Higher-quality quantized medium model"
  "large-v1|Legacy large multilingual model"
  "large-v2|Large multilingual model"
  "large-v2-q5_0|Quantized large-v2 model"
  "large-v2-q8_0|Higher-quality quantized large-v2 model"
  "large-v3|Recommended default"
  "large-v3-q5_0|Quantized large-v3 model"
  "large-v3-turbo|Fast large-class model"
  "large-v3-turbo-q5_0|Quantized large-v3-turbo model"
  "large-v3-turbo-q8_0|Higher-quality quantized large-v3-turbo model"
)
MODEL_NAME="${WHISPER_MODEL:-}"
MODEL_BIN_BASENAME=""
MODEL_ENCODER_BASENAME=""
VAD_MODEL_BASENAME="ggml-${VAD_MODEL_NAME}.bin"
MODEL_LINK_BASENAME="ggml-model.bin"
MODEL_ENCODER_LINK_BASENAME="ggml-model-encoder.mlmodelc"

cd "${PROJECT_ROOT}"

show_usage() {
  cat <<EOF
Usage:
  $(basename "$0") [--model <name>]
  $(basename "$0") [<model-name>]

Purpose:
  Build whisper.cpp with CoreML support on macOS and install the runtime into:
    ${RUNTIME_DIR}

Model Selection:
  - If --model or a positional model name is provided, that model is installed.
  - If neither is provided and stdin is interactive, the script validates the environment first,
    then shows a numbered model list for keyboard selection.
  - If no value is entered, the default model is: ${DEFAULT_MODEL_NAME}
  - Quantized models reuse the CoreML encoder generated from their base model.

Options:
  -m, --model <name>   Whisper model to install.
  -h, --help           Show this help message.

Environment:
  WHISPER_MODEL        Default model name when --model is not provided.
  WHISPER_CA_BUNDLE    Optional custom CA bundle path for TLS-inspecting networks.

Runtime Output:
  - Actual model file: whisper/models/ggml-<model>.bin
  - Model symlink:     whisper/models/${MODEL_LINK_BASENAME}
  - Encoder symlink:   whisper/models/${MODEL_ENCODER_LINK_BASENAME}
  - VAD model:         whisper/models/${VAD_MODEL_BASENAME}

Behavior:
  - The runtime directory (${RUNTIME_DIR}) is removed and recreated during install.
  - Existing runtime files and previously installed models under ${RUNTIME_DIR} are replaced.
  - Temporary build artifacts are created under:
      ${INSTALL_VENV_DIR}
      ${WHISPER_BUILD_DIR}
    and removed on exit.

Examples:
  bash install_macos.sh
  bash install_macos.sh --model large-v3-q5_0
  bash install_macos.sh --model medium
  bash install_macos.sh large-v3-turbo
  WHISPER_MODEL=small bash install_macos.sh
EOF
}

trim_string() {
  printf '%s' "$1" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//'
}

print_installable_models() {
  local i=1
  local spec
  local name
  local desc
  echo "======Installable Whisper models======"
  for spec in "${AVAILABLE_MODELS[@]}"; do
    name="${spec%%|*}"
    desc="${spec#*|}"
    printf "  %2d) %-10s %s\n" "${i}" "${name}" "${desc}"
    ((i++))
  done
}

is_supported_model() {
  local model="$1"
  local spec
  for spec in "${AVAILABLE_MODELS[@]}"; do
    if [[ "${spec%%|*}" == "${model}" ]]; then
      return 0
    fi
  done
  return 1
}

normalize_model_name() {
  case "$1" in
    turbo)
      printf '%s' "large-v3-turbo"
      ;;
    turbo-q5_0)
      printf '%s' "large-v3-turbo-q5_0"
      ;;
    turbo-q8_0)
      printf '%s' "large-v3-turbo-q8_0"
      ;;
    *)
      printf '%s' "$1"
      ;;
  esac
}

resolve_coreml_model_name() {
  case "$1" in
    *-q5_0)
      printf '%s' "${1%-q5_0}"
      ;;
    *-q5_1)
      printf '%s' "${1%-q5_1}"
      ;;
    *-q8_0)
      printf '%s' "${1%-q8_0}"
      ;;
    *)
      printf '%s' "$1"
      ;;
  esac
}

finalize_model_selection() {
  MODEL_NAME="$(normalize_model_name "$(trim_string "${MODEL_NAME}")")"
  MODEL_NAME="${MODEL_NAME:-${DEFAULT_MODEL_NAME}}"
  if [[ -z "${MODEL_NAME}" ]]; then
    echo "[ERROR] Model name must not be empty."
    exit 1
  fi
  if ! is_supported_model "${MODEL_NAME}"; then
    echo "[ERROR] Unsupported Whisper model: ${MODEL_NAME}"
    print_installable_models
    exit 1
  fi

  MODEL_BIN_BASENAME="ggml-${MODEL_NAME}.bin"
  MODEL_ENCODER_BASENAME="ggml-$(resolve_coreml_model_name "${MODEL_NAME}")-encoder.mlmodelc"
  echo "[OK] Selected Whisper model: ${MODEL_NAME}"
  if [[ "${MODEL_ENCODER_BASENAME}" != "ggml-${MODEL_NAME}-encoder.mlmodelc" ]]; then
    echo "[OK] CoreML encoder source model: $(resolve_coreml_model_name "${MODEL_NAME}")"
  fi
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -m|--model)
        if [[ $# -lt 2 ]]; then
          echo "[ERROR] Missing value for $1"
          exit 1
        fi
        MODEL_NAME="$2"
        shift 2
        ;;
      -h|--help)
        show_usage
        exit 0
        ;;
      --)
        shift
        break
        ;;
      -*)
        echo "[ERROR] Unknown option: $1"
        show_usage
        exit 1
        ;;
      *)
        if [[ -n "${MODEL_NAME}" ]]; then
          echo "[ERROR] Multiple model names provided."
          show_usage
          exit 1
        fi
        MODEL_NAME="$1"
        shift
        ;;
    esac
  done

  if [[ $# -gt 0 ]]; then
    echo "[ERROR] Unexpected arguments: $*"
    show_usage
    exit 1
  fi
}

select_model() {
  local selection=""
  local max_index="${#AVAILABLE_MODELS[@]}"

  if [[ -n "${MODEL_NAME}" ]]; then
    finalize_model_selection
    return
  fi

  if [[ ! -t 0 ]]; then
    MODEL_NAME="${DEFAULT_MODEL_NAME}"
    echo "[OK] Non-interactive shell detected. Using default Whisper model: ${MODEL_NAME}"
    finalize_model_selection
    return
  fi

  print_installable_models
  echo
  while true; do
    read -r -p "Select model number [default: ${DEFAULT_MODEL_NAME}]: " selection
    selection="$(trim_string "${selection}")"
    if [[ -z "${selection}" ]]; then
      MODEL_NAME="${DEFAULT_MODEL_NAME}"
      break
    fi
    if [[ "${selection}" =~ ^[0-9]+$ ]] && (( selection >= 1 && selection <= max_index )); then
      MODEL_NAME="${AVAILABLE_MODELS[selection-1]%%|*}"
      break
    fi
    echo "[ERROR] Invalid selection. Enter a number between 1 and ${max_index}."
  done

  finalize_model_selection
}

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

check_install_environment() {
  echo "======Checking install environment...======"

  if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "[ERROR] CoreML build is supported only on macOS."
    exit 1
  fi

  if ! command -v python3 >/dev/null 2>&1; then
    echo "[ERROR] python3 not found."
    exit 1
  fi

  if ! command -v git >/dev/null 2>&1; then
    echo "[ERROR] git not found."
    exit 1
  fi

  if ! command -v cmake >/dev/null 2>&1; then
    echo "[ERROR] cmake not found."
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

  echo "[OK] Install environment validated."
}

parse_args "$@"
check_install_environment
select_model
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

echo "======Downloading GGML model (${MODEL_NAME})...======"
bash ./models/download-ggml-model.sh "${MODEL_NAME}"

COREML_MODEL_NAME="$(resolve_coreml_model_name "${MODEL_NAME}")"

echo "======Generating CoreML model (${COREML_MODEL_NAME})...======"
bash ./models/generate-coreml-model.sh "${COREML_MODEL_NAME}"

echo "======Downloading VAD model (${VAD_MODEL_NAME})...======"
bash ./models/download-vad-model.sh "${VAD_MODEL_NAME}"

echo "======Preparing runtime files in ./whisper...======"
rm -rf "${RUNTIME_DIR}"
mkdir -p "${RUNTIME_DIR}/bin" "${RUNTIME_DIR}/lib" "${RUNTIME_DIR}/models"

cp -f build/bin/whisper-cli "${RUNTIME_DIR}/bin/"
cp -a build/src/libwhisper*.dylib "${RUNTIME_DIR}/lib/"
cp -a build/ggml/src/libggml*.dylib "${RUNTIME_DIR}/lib/"
cp -a build/ggml/src/ggml-blas/libggml-blas*.dylib "${RUNTIME_DIR}/lib/"
cp -a build/ggml/src/ggml-metal/libggml-metal*.dylib "${RUNTIME_DIR}/lib/"

cp -f "models/${MODEL_BIN_BASENAME}" "${RUNTIME_DIR}/models/"
cp -f "models/${VAD_MODEL_BASENAME}" "${RUNTIME_DIR}/models/"
cp -a "models/${MODEL_ENCODER_BASENAME}" "${RUNTIME_DIR}/models/"
ln -sfn "${MODEL_BIN_BASENAME}" "${RUNTIME_DIR}/models/${MODEL_LINK_BASENAME}"
ln -sfn "${MODEL_ENCODER_BASENAME}" "${RUNTIME_DIR}/models/${MODEL_ENCODER_LINK_BASENAME}"

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
  -m "whisper/models/${MODEL_LINK_BASENAME}" -l ko \
  --vad \
  --vad-model "whisper/models/${VAD_MODEL_BASENAME}" \
  --suppress-nst \
  --vad-threshold 0.1 \
  --vad-min-speech-duration-ms 500 \
  --vad-min-silence-duration-ms 500 \
  --vad-max-speech-duration-s 20 \
  --vad-speech-pad-ms 150 \
  --vad-samples-overlap 0.05 \
  --no-speech-thold 0.1 \
  --logprob-thold -0.8 \
  --max-context  0 \
  --output-txt \
  --output-json \
  sample/test_stt.wav
