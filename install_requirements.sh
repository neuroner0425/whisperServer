#!/bin/bash

pip install --upgrade --pre torch torchvision torchaudio --extra-index-url https://download.pytorch.org/whl/nightly/cpu
pip install flask whisper werkzeug tqdm