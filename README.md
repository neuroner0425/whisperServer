# WhisperServer

음성/영상 파일을 업로드하여 Whisper로 STT(음성 인식) 결과를 확인하고, 정제본/원본 텍스트를 다운로드할 수 있는 **Go Echo 기반** 서비스입니다.

## 주요 기능

- Whisper STT: 업로드 파일을 wav로 변환 후 순차 처리
- 로컬 DB 저장: 작업 메타데이터/업로드 wav/전사 결과/정제 결과를 SQLite(`.run/whisper.db`)에 저장
- 작업 큐/상태 페이지: 대기/진행/정제/완료 상태 실시간 폴링
- Gemini 정제(선택): 전사 결과 문장 정제 및 재정제
- 단건/일괄 다운로드, 일괄 삭제
- Prometheus `/metrics`, 헬스체크 `/healthz`

## 사전 요구 사항

- Go 1.25+
- `ffmpeg`, `ffprobe`
- whisper runtime 파일
  - `whisper/bin/whisper-cli`
  - `whisper/models/ggml-model.bin` (선택한 모델을 가리키는 심볼릭 링크)
  - `whisper/models/ggml-model-encoder.mlmodelc` (선택한 CoreML encoder를 가리키는 심볼릭 링크)
  - `whisper/models/ggml-silero-v6.2.0.bin`

## macOS 설치

기본 실행 시 먼저 설치 환경(macOS, Python, git, cmake, Xcode/CoreML 도구)을 검사한 뒤, 설치 가능한 Whisper 모델 목록을 번호로 보여주고 키보드로 선택하게 합니다. 아무 값도 입력하지 않으면 `large-v3`를 사용합니다.
설치 스크립트는 선택한 실제 모델 파일을 복사한 뒤, 서버가 항상 `ggml-model.bin` 링크만 사용하도록 설정합니다. `large-v3-q5_0` 같은 양자화 모델을 선택하면, CoreML encoder는 대응하는 원본 모델(`large-v3`) 기준으로 생성해 링크합니다.
설치 중 `whisper/` 런타임 디렉터리는 통째로 다시 생성되므로, 기존에 설치된 모델과 링크는 새 선택값으로 교체됩니다.

```bash
bash install_macos.sh
```

모델명을 바로 지정할 수도 있습니다.

```bash
bash install_macos.sh --model medium
```

양자화 모델도 직접 지정할 수 있습니다.

```bash
bash install_macos.sh --model large-v3-q5_0
```

위치는 옵션 없이 위치 인자로도 줄 수 있습니다.

```bash
bash install_macos.sh large-v3-turbo
```

환경 변수로 기본 모델과 CA 번들을 지정할 수도 있습니다.

```bash
WHISPER_MODEL=small WHISPER_CA_BUNDLE=/path/to/company-ca.pem bash install_macos.sh
```

`bash install_macos.sh --help` 를 실행하면 현재 스크립트 기준 사용법이 출력됩니다.

## 실행

```bash
go mod tidy
go run ./src/cmd/server
```

기본 설정은 루트의 `app.conf.default`에서 읽습니다.
`app.conf` 파일이 존재하면 `app.conf`를 우선 사용합니다.

## 설정 파일

- `PORT` (기본: `8000`)
- `MAX_UPLOAD_SIZE_MB` (기본: `512`)
- `UPLOAD_RATE_LIMIT_KBPS` (기본: `0`)
  - `0`: 제한 없음
  - 양수: 클라이언트 요청당 업로드 속도 제한(KB/s)
- `JOB_TIMEOUT_SEC` (기본: `3600`)
- `GEMINI_MODEL` (기본: `gemini-3.1-flash-lite-preview`)
- `SPLIT_TRANSCRIBE_REFINE_QUEUE` (기본: `false`)
  - `false`: 단일 큐(전사/정제 직렬)
  - `true`: 전사 큐(1개) + 정제 큐(1개) 분리 동작
- `GEMINI_API_KEYS` (JSON 배열 문자열, 예: `["key1","key2"]`)
- `JWT_SECRET` (권장: 32바이트 이상 랜덤 문자열)
- `JWT_ISSUER` (기본: `whisperserver`)
- `JWT_EXP_HOURS` (기본: `24`)
- `AUTH_COOKIE_SECURE` (기본: `false`, HTTPS 환경에서는 `true` 권장)

`app.conf`는 `.gitignore`에 포함되어 Git에 올라가지 않습니다.

## SPA 프런트엔드 개발

Vite 기반 SPA는 `frontend/`에 추가됩니다.

```bash
cd frontend
npm install
npm run build
```

빌드 결과물은 `static/app/`에 생성되며, 서버 실행 후 [http://localhost:8000/app](http://localhost:8000/app) 에서 확인할 수 있습니다.

## 엔드포인트

- `GET /` 홈
- `GET /upload`, `POST /upload`
- `GET /jobs`
- `GET /job/:job_id`
- `GET /status/:job_id`
- `POST /job/:job_id/refine`
- `GET /download/:job_id`
- `GET /download/:job_id/refined`
- `POST /batch-download`
- `POST /batch-delete`
- `GET /healthz`
- `GET /metrics`

## 프로젝트 구조

```text
whisperServer/
├── origin/                # 기존 Python(FastAPI) 코드 보관
├── go.mod
├── go.sum
├── src/
│   ├── cmd/server/main.go         # Echo 서버 엔트리포인트
│   └── internal/app/
│       ├── run.go                 # 부트스트랩/라우트 등록
│       ├── handlers.go            # HTTP 핸들러
│       ├── worker.go              # 전사/정제 워커
│       ├── storage.go             # jobs.json 영속화/작업 상태 관리
│       ├── media.go               # 업로드/ffmpeg/ffprobe 유틸
│       ├── text.go                # 결과 텍스트 렌더링
│       ├── util.go                # 공통 헬퍼
│       ├── gemini.go              # Gemini 정제 클라이언트
│       ├── globals.go             # 상수/전역 상태
│       └── app.go                 # 패키지 엔트리 파일
├── templates/
├── static/
├── jobs.json
└── whisper/
```
