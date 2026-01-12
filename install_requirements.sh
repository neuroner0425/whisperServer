# !/bin/bash

pip install --upgrade pip

echo "======Installing PyTorch, torchvision, torchaudio...======"
pip install torch torchvision torchaudio --index-url https://download.pytorch.org/whl/cpu

echo "======Installing OpenAI Whisper...======"
pip install git+https://github.com/openai/whisper.git

echo "======Installing Flask, Werkzeug, tqdm...======"
pip install flask werkzeug tqdm
pip install -r requirements.txt

echo "======Cloning whisper.cpp repository...======"
git clone https://github.com/ggerganov/whisper.cpp.git

cd whisper.cpp

echo "======Installing CoreML requirements...======"
pip install -r models/requirements-coreml.txt

echo "======Configuring CMake for CoreML...======"
cmake -B build -DWHISPER_COREML=1

echo "======Building with CMake...======"
cmake --build build --config Release

echo "======Downloading GGML model (large)...======"
bash ./models/download-ggml-model.sh large-v3

echo "======Generating CoreML model (large)...======"
./models/generate-coreml-model.sh large-v3

echo "======Downloading VAD model (silero)...======"
bash ./models/download-vad-model.sh silero-v5.1.2

echo "======All steps completed.======"

cd ..

echo "======Testing whisper.cpp with a sample audio file...======"

./whisper.cpp/build/bin/whisper-cli \
  -m whisper.cpp/models/ggml-large-v3.bin -l ko \
  --vad \
  --vad-model whisper.cpp/models/ggml-silero-v5.1.2.bin \
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
