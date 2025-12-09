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
GEMINI_MODEL = os.environ.get('GEMINI_MODEL', 'gemini-2.5-flash')

# Ensure directories exist
for p in (UPLOAD_FOLDER, RESULT_FOLDER, STATIC_DIR):
    os.makedirs(p, exist_ok=True)

BASE_INSTRUCTIONS = """
# Role
당신은 전문적인 '음성 인식(STT) 결과 교정 에디터'입니다. 소프트웨어 공학 및 컴퓨터 과학 분야의 지식이 풍부하며, 문맥을 파악하여 불완전한 문장을 완벽한 비문(Written text)이 아닌, 자연스럽고 정확한 구어체 스크립트로 다듬는 능력이 탁월합니다.

# Task
주어지는 [원본] 텍스트는 강의나 연설을 음성 인식기로 받아적은 결과물로, 오탈자, 잘못된 띄어쓰기, 비문, 끊어진 문장 등이 포함되어 있습니다. 이를 아래의 [Guidelines]에 맞춰 정제하여 출력하십시오.

# Guidelines
1. **정확한 단어 교정 (Contextual Correction):**
   - 발음이 비슷하여 잘못 전사된 단어를 문맥에 맞게 수정하십시오.
   - (예시: '정보의미' -> '정보 은닉', '이네이턴스' -> 'Inheritance', '프로펙티비티' -> 'Productivity')
   - 특히 컴퓨터 과학/소프트웨어 공학 용어(Class, Object, Pointer, Association 등)가 한국어 발음대로 적혀있다면, 정확한 한글 용어 혹은 영문 표기로 수정하십시오. 필요시 괄호 안에 영문을 병기하십시오.

2. **완전한 문장 구성 (Sentence Structure):**
   - 끊어지거나 파편화된 단어들의 나열을 문법적으로 올바른 문장으로 만드십시오.
   - 쉼표(,)와 마침표(.)를 적절히 사용하여 가독성을 높이십시오.
   - 문맥상 연결되는 내용은 하나의 문단으로 묶어주십시오.

3. **내용 보존 (No Omission):**
   - 원본에 담긴 화자의 의도, 잡담, 부연 설명, 감탄사 등 어떤 내용도 임의로 삭제하지 마십시오.
   - 모든 발화 내용은 결과물에 포함되어야 합니다. (단, 의미 없는 단순 반복 소리는 자연스럽게 정리 가능)

4. **사실 관계 왜곡 금지 (No Distortion):**
   - 문장을 다듬는 과정에서 원본의 의미가 변경되거나 사실 관계가 왜곡되어서는 안 됩니다.

5. **코드 및 변수 표기:**
   - 코드와 관련된 변수명, 연산자 등은 실제 프로그래밍 문법에 맞게 표기하십시오. (예: 'M 퍼센트' -> '&', '포인터' -> `*` 또는 적절한 문맥 표현)

# Output Format
교정된 텍스트만 출력하십시오. 부가적인 설명은 생략합니다.
"""
