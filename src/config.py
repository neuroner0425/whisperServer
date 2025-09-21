import os
from pathlib import Path

BASE_DIR = os.path.dirname(os.path.abspath(__file__))  # src 디렉토리
PROJECT_ROOT = os.path.dirname(BASE_DIR)
UPLOAD_FOLDER = os.path.join(PROJECT_ROOT, 'uploads')
RESULT_FOLDER = os.path.join(PROJECT_ROOT, 'results')
TEMPLATE_DIR = os.path.join(PROJECT_ROOT, 'templates')
STATIC_DIR = os.path.join(PROJECT_ROOT, 'static')
MODEL_DIR = os.path.join(PROJECT_ROOT, 'whisper.cpp', 'models')
WHISPER_CLI = os.path.join(PROJECT_ROOT, 'whisper.cpp', 'build', 'bin', 'whisper-cli')

ALLOWED_EXTENSIONS = {'mp3', 'mp4', 'wav', 'm4a'}
CHUNK_SIZE = 4 * 1024 * 1024  # 4MB
MAX_UPLOAD_SIZE_MB = int(os.environ.get('MAX_UPLOAD_SIZE_MB', '512'))
JOB_TIMEOUT_SEC = int(os.environ.get('JOB_TIMEOUT_SEC', '3600'))
GEMINI_MODEL = os.environ.get('GEMINI_MODEL', 'gemini-2.5-pro')

# Ensure directories exist
for p in (UPLOAD_FOLDER, RESULT_FOLDER, STATIC_DIR):
    os.makedirs(p, exist_ok=True)
