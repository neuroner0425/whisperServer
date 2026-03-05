#!/bin/bash

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")" && pwd)"
WHISPER_BUILD_DIR="${PROJECT_ROOT}/whisper.cpp"
RUNTIME_DIR="${PROJECT_ROOT}/whisper"

cd "${PROJECT_ROOT}"

pip install --upgrade pip

echo "======Installing PyTorch, torchvision, torchaudio...======"
pip install torch torchvision torchaudio --index-url https://download.pytorch.org/whl/cpu

echo "======Installing OpenAI Whisper...======"
pip install git+https://github.com/openai/whisper.git

echo "======Installing Flask, Werkzeug, tqdm...======"
pip install flask werkzeug tqdm
pip install -r requirements.txt

echo "======Cloning whisper.cpp repository...======"
rm -rf "${WHISPER_BUILD_DIR}"
git clone https://github.com/ggerganov/whisper.cpp.git

cd "${WHISPER_BUILD_DIR}"

echo "======Installing CoreML requirements...======"
pip install -r models/requirements-coreml.txt

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
