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
  - `whisper/models/ggml-large-v3.bin`
  - `whisper/models/ggml-silero-v6.2.0.bin`

## 실행

```bash
go mod tidy
go run ./src/cmd/server
```

기본 포트는 `8000`이며 `PORT` 환경변수로 변경할 수 있습니다.

## 환경 변수

- `PORT` (기본: `8000`)
- `MAX_UPLOAD_SIZE_MB` (기본: `512`)
- `JOB_TIMEOUT_SEC` (기본: `3600`)
- `GEMINI_MODEL` (기본: `gemini-2.5-flash`)
- `GEMINI_API_KEY` 또는 `API_KEY`
- `JWT_SECRET` (권장: 32바이트 이상 랜덤 문자열)
- `JWT_EXP_HOURS` (기본: `24`)
- `AUTH_COOKIE_SECURE` (기본: `false`, HTTPS 환경에서는 `true` 권장)

Gemini 키 파일도 지원합니다.

- `gemini_api_key.txt`
- `.gemini_api_key`

(파일 내 여러 키를 줄바꿈으로 넣으면 라운드로빈 사용)

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
