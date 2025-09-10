# WhisperServer

음성/영상 파일을 업로드하여 Whisper로 STT(음성 인식) 결과를 웹에서 확인하고, 타임라인별로 텍스트를 다운로드할 수 있는 FastAPI 기반 서비스입니다.

---

## 주요 기능

- Whisper STT: OpenAI Whisper 모델로 음성 인식
- 업로드 → 작업 큐에 저장 → 순차 처리(작업별 모델 로드/해제)로 안정성 보장
- 결과 txt 다운로드

---

## 설치 및 실행

### 1. Python 환경 준비

```bash
python -m venv .venv
source .venv/bin/activate
```

### 2. 종속성 설치

```bash
pip install -r requirements.txt
```

또는 레거시 스크립트를 사용하려면:

```bash
./install_requirements.sh
```

### 3. 서버 실행(권장)

프로덕션에서는 포크/멀티프로세스 관련 MPS 이슈를 피하기 위해 단일 워커로 실행하세요:

```bash
export PYTORCH_ENABLE_MPS_FALLBACK=1
# 필요시 강제 CPU 모드
export FORCE_CPU=1
uvicorn app:app --host 0.0.0.0 --port 8000 --workers 1
```

개발 중에는 아래처럼 직접 실행할 수 있습니다(간단한 테스트용):

```bash
python app.py
```

`--reload` 옵션은 프로덕션에서 사용하지 마세요. MPS 초기화 문제를 유발할 수 있습니다.

## 프로젝트 구조

```
whisperServer/
├── app.py
├── job_persist.py
├── install_requirements.sh
├── requirements.txt
├── pyproject.toml
├── uploads/
├── results/
├── jobs.json
├── static/
└── templates/
```

---

## 운영 권장

- 업로드 크기 제한 및 MIME 검사 도입
- graceful shutdown 구현(on_shutdown) 권장
- 로깅 개선(logging 모듈), 모니터링/헬스 체크 추가
- production 배포 시 Docker 이미지 사용 고려

---

## 개발 및 테스트

- 테스트 실행: `pytest`

---

필요하면 `Dockerfile`과 `on_shutdown` 훅, 또는 `requirements.txt` 고정/검증을 바로 적용해 드리겠습니다.