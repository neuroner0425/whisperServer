#!/bin/bash

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")" && pwd)"
WHISPER_BUILD_DIR="${PROJECT_ROOT}/whisper.cpp"
RUNTIME_DIR="${PROJECT_ROOT}/whisper"
INSTALL_VENV_DIR="${PROJECT_ROOT}/.install_venv"
BIN_DIR="${PROJECT_ROOT}/bin"
BIN_FILE="${BIN_DIR}/server-bin"
FRONTEND_DIR="${PROJECT_ROOT}/frontend"
FRONTEND_DIST_DIR="${PROJECT_ROOT}/static/app"
DEFAULT_MODEL_NAME="large-v3"
VAD_MODEL_NAME="silero-v6.2.0"
VAD_MODEL_BASENAME="ggml-${VAD_MODEL_NAME}.bin"
MODEL_LINK_BASENAME="ggml-model.bin"
MODEL_ENCODER_LINK_BASENAME="ggml-model-encoder.mlmodelc"
WHISPER_PYTHON_PACKAGES=(
  "torch"
  "openai-whisper"
  "ane_transformers"
  "coremltools"
)
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
WHISPER_MODE="auto"
BUILD_MODE="auto"
INSTALL_WHISPER=0
BUILD_FRONTEND=0
BUILD_BACKEND=0
RESET_FRONTEND_BUILD=0
RESET_BACKEND_BUILD=0

cd "${PROJECT_ROOT}"
mkdir -p "${BIN_DIR}"

show_usage() {
  cat <<EOF
Usage:
  $(basename "$0") [--model <name>]
  $(basename "$0") [<model-name>]

Purpose:
  Install whisper.cpp runtime with CoreML support and optionally build the app outputs:
    whisper runtime: ${RUNTIME_DIR}
    backend binary:  ${BIN_FILE}
    frontend build:  ${FRONTEND_DIST_DIR}

Model Selection:
  - Model selection is only used when Whisper is newly installed or reinstalled.
  - If --model or a positional model name is provided, that model is installed.
  - If neither is provided and stdin is interactive, the script shows a numbered model list.
  - If no value is entered, the default model is: ${DEFAULT_MODEL_NAME}
  - Quantized models reuse the CoreML encoder generated from their base model.

Options:
  -m, --model <name>   Whisper model to install.
  --skip-whisper       Keep existing Whisper runtime and skip Whisper installation.
  --force-whisper      Install or reinstall Whisper without asking.
  --skip-build         Skip frontend and backend builds.
  --force-build        Build frontend and backend without asking.
  --whisper-only       Equivalent to --force-whisper --skip-build.
  --build-only         Equivalent to --skip-whisper --force-build.
  --all                Equivalent to --force-whisper --force-build.
  --none               Equivalent to --skip-whisper --skip-build.
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
  - The script first checks the base execution environment.
  - Whisper/CoreML toolchain checks run only if Whisper installation is selected.
  - Go/npm checks run only if frontend/backend build is selected.
  - If Whisper runtime already exists, it asks whether to skip or reinstall it. Default is skip.
  - If frontend/backend builds already exist, it asks whether to skip or rebuild them. Default is skip.
  - Reinstalling Whisper removes and recreates ${RUNTIME_DIR}.
  - Temporary build artifacts are created under:
      ${INSTALL_VENV_DIR}
      ${WHISPER_BUILD_DIR}
    and removed on exit.

Examples:
  bash install_macos.sh
  bash install_macos.sh --model large-v3-q5_0
  bash install_macos.sh --build-only
  bash install_macos.sh --whisper-only
  bash install_macos.sh --none
  bash install_macos.sh --model medium
  bash install_macos.sh large-v3-turbo
  WHISPER_MODEL=small bash install_macos.sh
EOF
}

die() {
  echo "[ERROR] $*" >&2
  exit 1
}

trim_string() {
  printf '%s' "$1" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//'
}

has_command() {
  command -v "$1" >/dev/null 2>&1
}

require_command() {
  has_command "$1" || die "$2"
}

prompt_yes_default_yes() {
  local prompt="$1"
  local answer=""

  if [[ ! -t 0 ]]; then
    echo "[OK] Non-interactive shell detected. Defaulting to yes: ${prompt}"
    return 0
  fi

  while true; do
    read -r -p "${prompt} [Y/n]: " answer
    answer="$(trim_string "${answer}")"
    answer="$(printf '%s' "${answer}" | tr '[:upper:]' '[:lower:]')"
    case "${answer}" in
      ""|y|yes) return 0 ;;
      n|no) return 1 ;;
      *) echo "[ERROR] Please answer Y or n." ;;
    esac
  done
}

set_whisper_mode() {
  local next_mode="$1"
  if [[ "${WHISPER_MODE}" != "auto" && "${WHISPER_MODE}" != "${next_mode}" ]]; then
    die "Conflicting Whisper mode options."
  fi
  WHISPER_MODE="${next_mode}"
}

set_build_mode() {
  local next_mode="$1"
  if [[ "${BUILD_MODE}" != "auto" && "${BUILD_MODE}" != "${next_mode}" ]]; then
    die "Conflicting build mode options."
  fi
  BUILD_MODE="${next_mode}"
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -m|--model)
        [[ $# -ge 2 ]] || die "Missing value for $1"
        MODEL_NAME="$2"
        shift 2
        ;;
      --skip-whisper)
        set_whisper_mode "skip"
        shift
        ;;
      --force-whisper)
        set_whisper_mode "force"
        shift
        ;;
      --skip-build)
        set_build_mode "skip"
        shift
        ;;
      --force-build)
        set_build_mode "force"
        shift
        ;;
      --whisper-only)
        set_whisper_mode "force"
        set_build_mode "skip"
        shift
        ;;
      --build-only)
        set_whisper_mode "skip"
        set_build_mode "force"
        shift
        ;;
      --all)
        set_whisper_mode "force"
        set_build_mode "force"
        shift
        ;;
      --none)
        set_whisper_mode "skip"
        set_build_mode "skip"
        shift
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
        show_usage
        die "Unknown option: $1"
        ;;
      *)
        [[ -z "${MODEL_NAME}" ]] || die "Multiple model names provided."
        MODEL_NAME="$1"
        shift
        ;;
    esac
  done

  [[ $# -eq 0 ]] || die "Unexpected arguments: $*"
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
    printf "  %2d) %-22s %s\n" "${i}" "${name}" "${desc}"
    ((i++))
  done
}

is_supported_model() {
  local model="$1"
  local spec

  for spec in "${AVAILABLE_MODELS[@]}"; do
    [[ "${spec%%|*}" == "${model}" ]] && return 0
  done
  return 1
}

normalize_model_name() {
  case "$1" in
    turbo) printf '%s' "large-v3-turbo" ;;
    turbo-q5_0) printf '%s' "large-v3-turbo-q5_0" ;;
    turbo-q8_0) printf '%s' "large-v3-turbo-q8_0" ;;
    *) printf '%s' "$1" ;;
  esac
}

resolve_coreml_model_name() {
  case "$1" in
    *-q5_0) printf '%s' "${1%-q5_0}" ;;
    *-q5_1) printf '%s' "${1%-q5_1}" ;;
    *-q8_0) printf '%s' "${1%-q8_0}" ;;
    *) printf '%s' "$1" ;;
  esac
}

finalize_model_selection() {
  MODEL_NAME="$(normalize_model_name "$(trim_string "${MODEL_NAME}")")"
  MODEL_NAME="${MODEL_NAME:-${DEFAULT_MODEL_NAME}}"

  [[ -n "${MODEL_NAME}" ]] || die "Model name must not be empty."
  is_supported_model "${MODEL_NAME}" || {
    print_installable_models
    die "Unsupported Whisper model: ${MODEL_NAME}"
  }

  MODEL_BIN_BASENAME="ggml-${MODEL_NAME}.bin"
  MODEL_ENCODER_BASENAME="ggml-$(resolve_coreml_model_name "${MODEL_NAME}")-encoder.mlmodelc"

  echo "[OK] Selected Whisper model: ${MODEL_NAME}"
  if [[ "${MODEL_ENCODER_BASENAME}" != "ggml-${MODEL_NAME}-encoder.mlmodelc" ]]; then
    echo "[OK] CoreML encoder source model: $(resolve_coreml_model_name "${MODEL_NAME}")"
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

whisper_runtime_exists() {
  [[ -x "${RUNTIME_DIR}/bin/whisper-cli" ]] &&
  [[ -e "${RUNTIME_DIR}/models/${MODEL_LINK_BASENAME}" ]] &&
  [[ -e "${RUNTIME_DIR}/models/${MODEL_ENCODER_LINK_BASENAME}" ]] &&
  [[ -f "${RUNTIME_DIR}/models/${VAD_MODEL_BASENAME}" ]]
}

frontend_project_exists() {
  [[ -d "${FRONTEND_DIR}" ]] && [[ -f "${FRONTEND_DIR}/package.json" ]]
}

frontend_build_exists() {
  [[ -f "${FRONTEND_DIST_DIR}/index.html" ]]
}

backend_build_exists() {
  [[ -x "${BIN_FILE}" ]]
}

decide_whisper_install() {
  echo "======Step 2/3: Checking Whisper runtime...======"
  INSTALL_WHISPER=0

  case "${WHISPER_MODE}" in
    skip)
      echo "[OK] Whisper installation disabled by option."
      return
      ;;
    force)
      INSTALL_WHISPER=1
      if whisper_runtime_exists; then
        echo "[OK] Whisper runtime exists and will be reinstalled because of the selected option."
      else
        echo "[OK] Whisper runtime will be installed because of the selected option."
      fi
      return
      ;;
  esac

  if ! whisper_runtime_exists; then
    INSTALL_WHISPER=1
    echo "[OK] Whisper runtime not found. A new installation will be performed."
    return
  fi

  if prompt_yes_default_yes "Whisper runtime already exists. Skip Whisper installation?"; then
    echo "[OK] Skipping Whisper installation."
    return
  fi

  INSTALL_WHISPER=1
  echo "[OK] Whisper runtime will be reinstalled."
}

plan_frontend_build() {
  if ! frontend_project_exists; then
    echo "[OK] Frontend project not found. Frontend build will be skipped."
    BUILD_FRONTEND=0
    RESET_FRONTEND_BUILD=0
    return
  fi

  BUILD_FRONTEND=1
  RESET_FRONTEND_BUILD=0

  if ! frontend_build_exists; then
    echo "[OK] Frontend build output not found. Frontend will be built."
    return
  fi

  case "${BUILD_MODE}" in
    force)
      RESET_FRONTEND_BUILD=1
      echo "[OK] Frontend build exists and will be rebuilt because of the selected option."
      ;;
    auto)
      if prompt_yes_default_yes "Frontend build already exists. Skip frontend build?"; then
        BUILD_FRONTEND=0
        echo "[OK] Skipping frontend build."
      else
        RESET_FRONTEND_BUILD=1
        echo "[OK] Frontend will be rebuilt."
      fi
      ;;
  esac
}

plan_backend_build() {
  BUILD_BACKEND=1
  RESET_BACKEND_BUILD=0

  if ! backend_build_exists; then
    echo "[OK] Backend binary not found. Backend will be built."
    return
  fi

  case "${BUILD_MODE}" in
    force)
      RESET_BACKEND_BUILD=1
      echo "[OK] Backend build exists and will be rebuilt because of the selected option."
      ;;
    auto)
      if prompt_yes_default_yes "Backend build already exists. Skip backend build?"; then
        BUILD_BACKEND=0
        echo "[OK] Skipping backend build."
      else
        RESET_BACKEND_BUILD=1
        echo "[OK] Backend will be rebuilt."
      fi
      ;;
  esac
}

decide_build_actions() {
  echo "======Step 3/3: Checking frontend/backend builds...======"
  BUILD_FRONTEND=0
  BUILD_BACKEND=0
  RESET_FRONTEND_BUILD=0
  RESET_BACKEND_BUILD=0

  if [[ "${BUILD_MODE}" == "skip" ]]; then
    echo "[OK] Frontend/backend builds disabled by option."
    return
  fi

  plan_frontend_build
  plan_backend_build
}

remove_path() {
  local target="$1"

  rm -rf "${target}" 2>/dev/null || true
  if [[ -d "${target}" ]]; then
    python3 - <<'PY' "${target}"
import shutil
import sys

shutil.rmtree(sys.argv[1], ignore_errors=True)
PY
  fi
}

cleanup_install_env() {
  set +e
  if [[ -n "${VIRTUAL_ENV:-}" ]]; then
    deactivate >/dev/null 2>&1 || true
  fi
  remove_path "${INSTALL_VENV_DIR}"
  remove_path "${WHISPER_BUILD_DIR}"
}

check_base_environment() {
  echo "======Step 1/3: Checking base execution environment...======"
  [[ "$(uname -s)" == "Darwin" ]] || die "CoreML build is supported only on macOS."
  echo "[OK] Base execution environment validated."
}

check_whisper_environment() {
  echo "======Checking Whisper install/download environment...======"

  require_command "python3" "python3 not found."
  require_command "git" "git not found."
  require_command "cmake" "cmake not found."
  require_command "xcode-select" "xcode-select not found. Please install Xcode and Command Line Tools."
  require_command "xcrun" "xcrun not found. Please install Command Line Tools."

  xcode-select -p >/dev/null 2>&1 || die $'Xcode path is not configured.\n        Run: sudo xcode-select --switch /Applications/Xcode.app/Contents/Developer'

  xcrun --find coremlc >/dev/null 2>&1 || die $'coremlc not found via xcrun.\n        This usually means full Xcode is missing or selected developer path is incorrect.\n        Check Xcode install and developer path:\n        1) xcode-select -p\n        2) sudo xcode-select --switch /Applications/Xcode.app/Contents/Developer'

  echo "[OK] Whisper install/download environment validated."
}

check_build_environment() {
  if [[ "${BUILD_FRONTEND}" -eq 0 && "${BUILD_BACKEND}" -eq 0 ]]; then
    return
  fi

  echo "======Checking build environment...======"

  if [[ "${BUILD_FRONTEND}" -eq 1 ]]; then
    require_command "npm" "npm not found, but frontend build is required."
  fi
  if [[ "${BUILD_BACKEND}" -eq 1 ]]; then
    require_command "go" "go not found, but backend build is required."
  fi

  echo "[OK] Build environment validated."
}

setup_install_env() {
  echo "======Creating temporary venv for installation...======"

  require_command "python3" "python3 not found."
  remove_path "${INSTALL_VENV_DIR}"
  python3 -m venv "${INSTALL_VENV_DIR}"

  # shellcheck disable=SC1091
  source "${INSTALL_VENV_DIR}/bin/activate"

  python -m pip install --upgrade pip certifi
  echo "[OK] Temporary venv activated: ${INSTALL_VENV_DIR}"
}

configure_ssl_environment() {
  local certifi_cafile=""

  echo "======Configuring SSL trust for model download...======"

  if [[ -n "${WHISPER_CA_BUNDLE:-}" ]]; then
    [[ -f "${WHISPER_CA_BUNDLE}" ]] || die "WHISPER_CA_BUNDLE is set but file does not exist: ${WHISPER_CA_BUNDLE}"

    export SSL_CERT_FILE="${WHISPER_CA_BUNDLE}"
    export REQUESTS_CA_BUNDLE="${WHISPER_CA_BUNDLE}"
    export CURL_CA_BUNDLE="${WHISPER_CA_BUNDLE}"
    export PIP_CERT="${WHISPER_CA_BUNDLE}"
    echo "[OK] Using custom CA bundle from WHISPER_CA_BUNDLE"
    return
  fi

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
    die $'TLS certificate verification failed while connecting to model host.\n        If your network uses TLS inspection, export WHISPER_CA_BUNDLE=/path/to/your/company-ca.pem\n        then rerun this script.\n        Temporary (unsafe) fallback only if unavoidable:\n        export PYTHONHTTPSVERIFY=0'
  fi

  echo "[OK] SSL trust check passed."
}

copy_matches_or_die() {
  local pattern="$1"
  local destination="$2"
  local matches=()

  while IFS= read -r match; do
    matches+=("${match}")
  done < <(compgen -G "${pattern}" || true)

  [[ "${#matches[@]}" -gt 0 ]] || die "Required runtime artifact not found: ${pattern}"
  cp -a "${matches[@]}" "${destination}/"
}

prepare_runtime_dir() {
  echo "======Preparing runtime files in ./whisper...======"

  rm -rf "${RUNTIME_DIR}"
  mkdir -p "${RUNTIME_DIR}/bin" "${RUNTIME_DIR}/lib" "${RUNTIME_DIR}/models"

  [[ -x "build/bin/whisper-cli" ]] || die "Built whisper-cli not found."
  [[ -f "models/${MODEL_BIN_BASENAME}" ]] || die "Downloaded model not found: models/${MODEL_BIN_BASENAME}"
  [[ -f "models/${VAD_MODEL_BASENAME}" ]] || die "Downloaded VAD model not found: models/${VAD_MODEL_BASENAME}"
  [[ -d "models/${MODEL_ENCODER_BASENAME}" ]] || die "Generated CoreML encoder not found: models/${MODEL_ENCODER_BASENAME}"

  cp -f "build/bin/whisper-cli" "${RUNTIME_DIR}/bin/"
  copy_matches_or_die "build/src/libwhisper*.dylib" "${RUNTIME_DIR}/lib"
  copy_matches_or_die "build/ggml/src/libggml*.dylib" "${RUNTIME_DIR}/lib"
  copy_matches_or_die "build/ggml/src/ggml-blas/libggml-blas*.dylib" "${RUNTIME_DIR}/lib"
  copy_matches_or_die "build/ggml/src/ggml-metal/libggml-metal*.dylib" "${RUNTIME_DIR}/lib"

  cp -f "models/${MODEL_BIN_BASENAME}" "${RUNTIME_DIR}/models/"
  cp -f "models/${VAD_MODEL_BASENAME}" "${RUNTIME_DIR}/models/"
  cp -a "models/${MODEL_ENCODER_BASENAME}" "${RUNTIME_DIR}/models/"
  ln -sfn "${MODEL_BIN_BASENAME}" "${RUNTIME_DIR}/models/${MODEL_LINK_BASENAME}"
  ln -sfn "${MODEL_ENCODER_BASENAME}" "${RUNTIME_DIR}/models/${MODEL_ENCODER_LINK_BASENAME}"
}

configure_runtime_rpaths() {
  local lib=""

  if ! otool -l "${RUNTIME_DIR}/bin/whisper-cli" | grep -Fq "@executable_path/../lib"; then
    install_name_tool -add_rpath "@executable_path/../lib" "${RUNTIME_DIR}/bin/whisper-cli"
  fi

  for lib in "${RUNTIME_DIR}/lib/"*.dylib; do
    if ! otool -l "${lib}" | grep -Fq "@loader_path"; then
      install_name_tool -add_rpath "@loader_path" "${lib}"
    fi
  done
}

test_whisper_runtime() {
  local sample_path="${PROJECT_ROOT}/sample/test_stt.wav"

  if [[ ! -f "${sample_path}" ]]; then
    echo "[OK] Sample audio not found. Runtime smoke test skipped: ${sample_path}"
    return
  fi

  echo "======Testing whisper runtime with a sample audio file...======"
  "${RUNTIME_DIR}/bin/whisper-cli" \
    -m "${RUNTIME_DIR}/models/${MODEL_LINK_BASENAME}" -l ko \
    --vad \
    --vad-model "${RUNTIME_DIR}/models/${VAD_MODEL_BASENAME}" \
    --suppress-nst \
    --vad-threshold 0.1 \
    --vad-min-speech-duration-ms 500 \
    --vad-min-silence-duration-ms 500 \
    --vad-max-speech-duration-s 20 \
    --vad-speech-pad-ms 150 \
    --vad-samples-overlap 0.05 \
    --no-speech-thold 0.1 \
    --logprob-thold -0.8 \
    --max-context 0 \
    --output-txt \
    --output-json \
    "${sample_path}"
}

install_whisper_runtime() {
  local coreml_model_name=""

  trap cleanup_install_env EXIT
  setup_install_env
  configure_ssl_environment

  echo "======Installing Python requirements for macOS/CoreML build...======"
  python -m pip install "${WHISPER_PYTHON_PACKAGES[@]}"

  echo "======Cloning whisper.cpp repository...======"
  remove_path "${WHISPER_BUILD_DIR}"
  git clone --depth 1 https://github.com/ggerganov/whisper.cpp.git "${WHISPER_BUILD_DIR}"

  cd "${WHISPER_BUILD_DIR}"

  echo "======Configuring CMake for CoreML...======"
  cmake -B build -DWHISPER_COREML=1

  echo "======Building with CMake...======"
  cmake --build build --config Release

  echo "======Downloading GGML model (${MODEL_NAME})...======"
  bash ./models/download-ggml-model.sh "${MODEL_NAME}"

  coreml_model_name="$(resolve_coreml_model_name "${MODEL_NAME}")"
  echo "======Generating CoreML model (${coreml_model_name})...======"
  bash ./models/generate-coreml-model.sh "${coreml_model_name}"

  echo "======Downloading VAD model (${VAD_MODEL_NAME})...======"
  bash ./models/download-vad-model.sh "${VAD_MODEL_NAME}"

  prepare_runtime_dir
  configure_runtime_rpaths

  cd "${PROJECT_ROOT}"
  remove_path "${WHISPER_BUILD_DIR}"
  test_whisper_runtime
}

build_frontend() {
  echo "======Building frontend...======"

  if [[ "${RESET_FRONTEND_BUILD}" -eq 1 ]] && [[ -d "${FRONTEND_DIST_DIR}" ]]; then
    rm -rf "${FRONTEND_DIST_DIR}"
  fi

  (
    cd "${FRONTEND_DIR}"
    if [[ ! -d node_modules ]]; then
      if [[ -f "package-lock.json" ]]; then
        npm ci
      else
        npm install
      fi
    fi
    npm run build
  )

  echo "[OK] Frontend build complete: ${FRONTEND_DIST_DIR}"
}

build_backend() {
  echo "======Building backend...======"

  if [[ "${RESET_BACKEND_BUILD}" -eq 1 ]] && [[ -f "${BIN_FILE}" ]]; then
    rm -f "${BIN_FILE}"
  fi

  (
    cd "${PROJECT_ROOT}"
    go build -o "${BIN_FILE}" ./src/cmd/server
  )

  echo "[OK] Backend build complete: ${BIN_FILE}"
}

main() {
  parse_args "$@"
  check_base_environment
  decide_whisper_install
  decide_build_actions

  if [[ "${INSTALL_WHISPER}" -eq 1 ]]; then
    check_whisper_environment
    select_model
    install_whisper_runtime
  fi

  check_build_environment

  if [[ "${BUILD_FRONTEND}" -eq 1 ]]; then
    build_frontend
  fi
  if [[ "${BUILD_BACKEND}" -eq 1 ]]; then
    build_backend
  fi

  if [[ "${INSTALL_WHISPER}" -eq 0 && "${BUILD_FRONTEND}" -eq 0 && "${BUILD_BACKEND}" -eq 0 ]]; then
    echo "======Nothing to do.======"
  else
    echo "======All selected steps completed.======"
  fi
}

main "$@"
