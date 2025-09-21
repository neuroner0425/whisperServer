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

사전 요구 사항:
- `ffmpeg`와 `ffprobe`가 시스템에 설치되어 있어야 합니다(macOS에서는 `brew install ffmpeg`).
- `whisper.cpp`가 `./whisper.cpp` 경로에 클론 및 빌드되어 있어야 하며 모델이 다운로드되어 있어야 합니다.
- 업로드 최대 용량은 환경변수 `MAX_UPLOAD_SIZE_MB`로 조정 가능합니다(기본 512MB).

### 3. 서버 실행(권장)

프로덕션에서는 포크/멀티프로세스 관련 MPS 이슈를 피하기 위해 단일 워커로 실행하세요:

```bash
export PYTORCH_ENABLE_MPS_FALLBACK=1
uvicorn src.app:app --host 0.0.0.0 --port 8000 --workers 1
```

또는 하위 호환을 위해 루트 `app.py`를 그대로 사용할 수도 있습니다:

```bash
python app.py  # 내부에서 src.app:app을 재노출
```

`--reload` 옵션은 프로덕션에서 사용하지 마세요. MPS 초기화 문제를 유발할 수 있습니다.

헬스 체크:

```bash
curl -s http://localhost:8000/healthz
```

## 프로젝트 구조

```
whisperServer/
├── app.py                  # 레거시 엔트리 (src.app re-export)
├── src/
│   ├── app.py             # FastAPI 엔트리포인트
│   ├── config.py          # 경로/환경 상수
│   ├── utils/
│   │   ├── media.py       # ffmpeg/ffprobe 헬퍼
│   │   └── text.py        # 포맷 유틸
│   ├── services/
│   │   └── gemini_service.py  # Gemini API 래퍼
│   ├── persistence/
│   │   └── jobs.py        # 작업 메모리 & 저장 위임
│   ├── workers/
│   │   └── whisper_worker.py  # STT 워커 스레드 & 실행 로직
│   └── job_persist.py         # 기존 JSON 원자적 저장 구현 (재사용)
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

### 모듈화 변경 요약

- 대형 `app.py` 를 역할별 파일로 분리하여 가독성과 유지보수성 향상
- 워커 로직(`whisper_worker.py`), Gemini 정제(`gemini_service.py`), 미디어 처리(`media.py`), 포맷/텍스트(`text.py`), 영속성 어댑터(`jobs.py`)로 책임 분리
- 루트 `app.py`는 하위 호환을 위한 re-export만 수행

필요하면 `Dockerfile`, 추가 로깅/메트릭 개선, 테스트 코드(예: 업로드/전사 e2e)도 확장 가능합니다. 요청 주세요.