# Code Map

기준 시각: 2026-04-06 (KST)

이 문서는 "모든 파일을 기계적으로 나열하는 목록"이 아니라, 처음 레포지토리를 보는 개발자가 어디를 읽어야 하는지 빠르게 판단할 수 있도록 만든 코드 맵이다.

## 1. 먼저 읽을 파일

백엔드 기준으로는 아래 순서가 가장 효율적이다.

1. [`src/cmd/server/main.go`](/Users/sh_kim/Library/Mobile Documents/com~apple~CloudDocs/workspace/whisperServer/src/cmd/server/main.go)
2. [`src/internal/server/run.go`](/Users/sh_kim/Library/Mobile Documents/com~apple~CloudDocs/workspace/whisperServer/src/internal/server/run.go)
3. [`src/internal/server/bootstrap.go`](/Users/sh_kim/Library/Mobile Documents/com~apple~CloudDocs/workspace/whisperServer/src/internal/server/bootstrap.go)
4. [`src/internal/server/bootstrap_services.go`](/Users/sh_kim/Library/Mobile Documents/com~apple~CloudDocs/workspace/whisperServer/src/internal/server/bootstrap_services.go)
5. [`src/internal/transport/http/routes.go`](/Users/sh_kim/Library/Mobile Documents/com~apple~CloudDocs/workspace/whisperServer/src/internal/transport/http/routes.go)

이 다섯 파일을 읽으면 현재 서버가 어떻게 시작되고, 어떤 서비스가 조립되고, 어떤 엔드포인트가 열리는지 전체 그림이 잡힌다.

## 2. 백엔드 패키지 맵

### `src/cmd/server`

- `main.go`
  - 서버 프로세스 진입점.
  - `internal/server`를 호출한다.

### `src/internal/server`

- 역할: composition root
- 핵심 파일:
  - `run.go`: 서버 start/shutdown lifecycle
  - `bootstrap.go`: 전체 bootstrap 생성
  - `bootstrap_services.go`: service/runtime/worker/integration 조립
  - `bootstrap_http.go`: Echo 생성과 공통 HTTP wiring
  - `http_handlers.go`: route table에 들어갈 handler 조립
  - `http_builders.go`: query/legacy handler builder
  - `renderer.go`: Echo renderer
  - `auth.go`, `config.go`, `paths.go`, `status.go`, `metrics.go`, `process_log.go`: bootstrap 보조

읽는 이유:
- "이 기능이 어디서 연결되는가?"를 찾을 때 시작점이 된다.

### `src/internal/transport/http`

- 역할: HTTP 경계
- 주요 파일:
  - `routes.go`: 전체 라우트 표
  - `route_paths.go`: transport 내부 경로 상수
  - `handlers_api_files.go`: 파일 목록/탐색 API
  - `handlers_api_job_detail.go`: 작업 상세 API
  - `handlers_api_job_control.go`: retry/refine/retranscribe API
  - `handlers_api_upload.go`: SPA 업로드 API
  - `handlers_api_trash.go`: 휴지통 API
  - `handlers_api_tags.go`: 태그 API
  - `handlers_api_storage.go`: 저장공간 API
  - `handlers_api_move.go`: batch move API
  - `handlers_api_folder_mutation.go`, `handlers_api_job_mutation.go`: 폴더/작업 mutation API
  - `handlers_sse.go`: SSE endpoint
  - `handlers_spa.go`: SPA entry / redirect
  - `handlers_legacy_*.go`: 구 HTML/polling/download 흐름 호환

읽는 이유:
- 요청 단위로 코드를 따라갈 때 가장 먼저 보는 패키지다.

### `src/internal/auth`

- 역할: 인증
- 핵심 파일:
  - `core.go`: JWT, middleware, 로그인/회원가입/로그아웃
  - `runtime.go`: server에서 쓰는 runtime wrapper
  - `core_test.go`: 인증 회귀 테스트

읽는 이유:
- 인증 정책, cookie, unauthorized 동작을 바꿀 때 기준점이다.

### `src/internal/domain`

- 역할: 핵심 도메인 타입
- 파일:
  - `job.go`
  - `job_status.go`
  - `folder.go`
  - `tag.go`
  - `user.go`

읽는 이유:
- 데이터 구조를 이해하려면 여기부터 보는 편이 빠르다.

### `src/internal/service`

- 역할: 유스케이스/흐름
- 주요 파일:
  - `upload_service.go`: 업로드 생성과 초기 job 생성
  - `folder_service.go`: 폴더 규칙과 조작
  - `tag_service.go`: 태그 생성/삭제/조회
  - `storage_service.go`: 저장공간 집계
  - `job_blob_service.go`: blob 접근 추상화
  - `job_lifecycle.go`: retry/reset/trash 관련 상태 전이
  - `job_restore.go`: 복구 시 재큐잉/복원 정책
  - `http_error.go`: transport와 맞물리는 서비스 에러 타입

읽는 이유:
- handler가 실제로 어떤 규칙을 수행하는지 보려면 이 계층을 읽는다.

### `src/internal/runtime`

- 역할: 메모리 런타임 상태
- 주요 파일:
  - `runtime.go`: runtime facade
  - `state_store.go`: in-memory job snapshot
  - `broker.go`: SSE broker
  - `runtime_test.go`: subtree/temp file 관련 테스트

읽는 이유:
- DB 바깥의 현재 상태, enqueue/requeue, SSE 이벤트를 이해할 때 필요하다.

### `src/internal/worker`

- 역할: 백그라운드 처리
- 주요 파일:
  - `worker.go`: 워커 루프와 공통 상태 전이
  - `worker_whisper.go`: 음성 전사 흐름
  - `worker_pdf.go`: PDF 문서 추출 흐름
  - `worker_pdf_test.go`: PDF worker 테스트

읽는 이유:
- 업로드 이후 실제 처리 파이프라인을 보려면 여기로 온다.

### `src/internal/integrations/gemini`

- 역할: Gemini API 연동
- 주요 파일:
  - `runtime.go`: client 관리, retry/cooldown, refine, extract
  - `document.go`: 문서 추출 결과 처리 보조
  - `document_test.go`: 문서 처리 회귀 테스트

### `src/internal/integrations/whisper`

- 역할: Whisper CLI 실행 어댑터
- 주요 파일:
  - `runtime.go`
  - `runtime_test.go`

### `src/internal/repo/sqlite`

- 역할: SQLite persistence
- 주요 파일:
  - `db_core.go`: DB 초기화, schema, migration, 공통 유틸
  - `db_jobs.go`: jobs snapshot
  - `db_blobs.go`: blob 저장소
  - `db_folders.go`: folders
  - `db_users_tags.go`: users, tags

읽는 이유:
- 실제 영속화 구조나 SQL을 바꿀 때 여기로 온다.

### `src/internal/query/files`

- 역할: 파일 화면 조회 모델 구성
- 주요 파일:
  - `query.go`
  - `query_test.go`

읽는 이유:
- `/api/files`, `/jobs/updates`, 휴지통/목록 화면 데이터가 어떻게 조립되는지 파악할 수 있다.

### `src/internal/queue`

- 역할: 큐 추상화
- 파일:
  - `queue.go`
  - `inmem.go`

### `src/internal/util`

- 역할: 공용 유틸과 외부 도구 실행 보조
- 파일:
  - `base.go`: 형변환/보정/검증
  - `media.go`: upload 저장, ffmpeg/ffprobe, duration 유틸
  - `pdf.go`: pdfinfo/pdftoppm 기반 PDF 유틸

### `src/internal/config`

- 역할: 설정 파싱 보조
- 파일:
  - `config.go`

### `src/internal/obs`

- 역할: 처리 로그
- 파일:
  - `processing_log.go`

## 3. 프론트엔드 코드 맵

### 앱 공통

- [`frontend/src/main.tsx`](/Users/sh_kim/Library/Mobile Documents/com~apple~CloudDocs/workspace/whisperServer/frontend/src/main.tsx)
  - React 진입점과 라우터 초기화
- [`frontend/src/AppShell.tsx`](/Users/sh_kim/Library/Mobile Documents/com~apple~CloudDocs/workspace/whisperServer/frontend/src/AppShell.tsx)
  - 인증 이후 공통 레이아웃
- [`frontend/src/styles.css`](/Users/sh_kim/Library/Mobile Documents/com~apple~CloudDocs/workspace/whisperServer/frontend/src/styles.css)
  - 전역 스타일
- [`frontend/src/usePageTitle.ts`](/Users/sh_kim/Library/Mobile Documents/com~apple~CloudDocs/workspace/whisperServer/frontend/src/usePageTitle.ts)
  - 화면 제목 훅
- [`frontend/src/AppErrorPage.tsx`](/Users/sh_kim/Library/Mobile Documents/com~apple~CloudDocs/workspace/whisperServer/frontend/src/AppErrorPage.tsx)
  - 전역 오류 화면

### 기능별

- `frontend/src/features/auth`
  - 로그인/회원가입 UI와 API
- `frontend/src/features/files`
  - 파일 목록, 폴더 탐색, 업로드, 드래그 이동
- `frontend/src/features/jobs`
  - 작업 상세, 오디오 재생, retry/refine UI
- `frontend/src/features/storage`
  - 저장공간 화면
- `frontend/src/features/tags`
  - 태그 관리
- `frontend/src/features/trash`
  - 휴지통 목록/복구/삭제

## 4. 기능별로 어디를 봐야 하나

### 파일 목록이 궁금할 때

1. `frontend/src/features/files/FilesPage.tsx`
2. `frontend/src/features/files/api.ts`
3. `src/internal/transport/http/handlers_api_files.go`
4. `src/internal/query/files/query.go`
5. `src/internal/service/folder_service.go`

### 업로드가 궁금할 때

1. `frontend/src/features/files/api.ts`
2. `src/internal/transport/http/handlers_api_upload.go`
3. `src/internal/service/upload_service.go`
4. `src/internal/worker/worker.go`
5. `src/internal/worker/worker_whisper.go` 또는 `worker_pdf.go`

### 작업 상세/재시도/정제가 궁금할 때

1. `frontend/src/features/jobs/JobDetailPage.tsx`
2. `frontend/src/features/jobs/api.ts`
3. `src/internal/transport/http/handlers_api_job_detail.go`
4. `src/internal/transport/http/handlers_api_job_control.go`
5. `src/internal/service/job_blob_service.go`
6. `src/internal/service/job_lifecycle.go`
7. `src/internal/integrations/gemini/runtime.go`

### 폴더/휴지통/태그가 궁금할 때

1. 관련 `frontend/src/features/*`
2. 관련 `transport/http` handler
3. `FolderService`, `TagService`
4. 필요 시 `runtime` subtree 처리
5. `repo/sqlite`

## 5. 현재 코드 읽기 원칙

- HTTP 요청부터 읽을 때는 `transport/http`에서 시작한다.
- 비즈니스 규칙은 `service`에서 찾는다.
- 메모리 상태/SSE/재큐잉은 `runtime`에서 찾는다.
- 실제 DB 저장은 `repo/sqlite`에서 찾는다.
- 외부 API/CLI 세부 구현은 `integrations/*`에서 찾는다.
- 조립과 wiring은 `server`에서만 본다.

이 기준을 따르면, 새 기능을 넣을 때도 "어느 패키지에 무엇을 둬야 하는지"를 비교적 안정적으로 판단할 수 있다.
