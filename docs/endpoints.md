# Endpoints

기준 시각: 2026-04-06 (KST)

이 문서는 현재 [`src/internal/transport/http/routes.go`](/Users/sh_kim/Library/Mobile Documents/com~apple~CloudDocs/workspace/whisperServer/src/internal/transport/http/routes.go) 기준의 실제 엔드포인트 목록이다.
처음 보는 개발자가 "어떤 URL이 어떤 흐름에 속하는지"를 빠르게 이해하는 것을 목표로 한다.

## 1. 전체 구분

현재 서버 엔드포인트는 크게 다섯 종류다.

1. 인증 폼 엔드포인트
2. SPA 진입 및 구경로 리다이렉트
3. 레거시 HTML form / polling / download 엔드포인트
4. JSON API
5. 운영용 엔드포인트

기본 정책:

- `/api/*`는 현재 프론트엔드가 직접 호출하는 JSON API다.
- `/upload`, `/jobs/updates`, `/download/*`, `/status/*`, `/batch-*` 등은 레거시 호환 흐름이다.
- 대부분의 파일/폴더/작업 엔드포인트는 인증이 필요하다.
- `/auth/login`, `/auth/join`, `/files/*`, `/file/:job_id`는 서버 렌더링 화면이 아니라 SPA entrypoint다.

## 2. 인증 엔드포인트

| Method | Path | 설명 |
|---|---|---|
| `GET` | `/login` | 로그인 SPA로 보냄 |
| `POST` | `/login` | 폼 로그인 처리 |
| `GET` | `/signup` | 회원가입 SPA로 보냄 |
| `POST` | `/signup` | 폼 회원가입 처리 |
| `POST` | `/logout` | 폼 로그아웃 처리 |
| `GET` | `/auth/login` | 로그인 SPA entry |
| `GET` | `/auth/join` | 회원가입 SPA entry |
| `POST` | `/api/auth/signup` | JSON 회원가입 |
| `POST` | `/api/auth/login` | JSON 로그인 |
| `POST` | `/api/auth/logout` | JSON 로그아웃 |
| `GET` | `/api/me` | 현재 사용자 조회 |

메모:

- 인증은 JWT cookie 기반이다.
- 브라우저 폼 흐름과 SPA JSON 흐름을 동시에 지원한다.
- 인증 실패 시:
  - HTML 요청은 로그인 페이지 redirect
  - JSON 요청은 `401` JSON

## 3. SPA 진입 / 리다이렉트

| Method | Path | 설명 |
|---|---|---|
| `GET` | `/` | 로그인 여부에 따라 `/files/home` 또는 `/auth/login`으로 이동 |
| `GET` | `/files` | `/files/home`으로 이동 |
| `GET` | `/files/home` | 홈 화면 SPA entry |
| `GET` | `/files/root` | 루트 파일 화면 SPA entry |
| `GET` | `/files/folder/:folder_id` | 특정 폴더 화면 SPA entry |
| `GET` | `/files/search` | 검색 화면 SPA entry |
| `GET` | `/files/trash` | 휴지통 화면 SPA entry |
| `GET` | `/files/storage` | 저장공간 화면 SPA entry |
| `GET` | `/file/:job_id` | 작업 상세 화면 SPA entry |
| `GET` | `/app` | SPA 직접 entry |
| `GET` | `/app/*` | SPA 직접 entry |
| `GET` | `/files/folders/:folder_id` | 구 폴더 URL을 새 URL로 정리 |
| `GET` | `/job/:job_id` | 구 작업 상세 URL을 새 URL로 정리 |
| `GET` | `/jobs` | 예전 목록 URL을 홈으로 정리 |
| `GET` | `/upload` | 예전 업로드 URL을 파일 화면으로 정리 |
| `GET` | `/trash` | 예전 휴지통 URL을 새 URL로 정리 |
| `GET` | `/tags` | 예전 태그 URL을 새 흐름으로 정리 |

설명:

- 현재 서버는 페이지별 HTML을 거의 직접 렌더링하지 않는다.
- 실제 화면 렌더링은 프론트엔드 SPA가 담당한다.
- 백엔드는 SPA `index.html`을 내려주거나, 과거 URL을 현재 URL로 연결하는 역할을 한다.

## 4. 레거시 HTML / 다운로드 / 폴링 엔드포인트

이 그룹은 예전 서버 주도 UI 또는 오래된 클라이언트와의 호환을 위해 남아 있다.

| Method | Path | 설명 |
|---|---|---|
| `POST` | `/upload` | 레거시 폼 업로드 |
| `GET` | `/jobs/updates` | 목록 갱신용 polling 응답 |
| `GET` | `/status/:job_id` | 단일 작업 상태 조회 |
| `GET` | `/download/:job_id` | 결과 다운로드 |
| `GET` | `/download/:job_id/refined` | 정제 결과 다운로드 |
| `GET` | `/download/:job_id/document-json` | PDF 문서 JSON 다운로드 |
| `POST` | `/batch-download` | 여러 결과 ZIP 다운로드 |
| `POST` | `/batch-delete` | 여러 작업/폴더 휴지통 이동 |
| `POST` | `/batch-move` | 여러 작업/폴더 이동 |
| `POST` | `/tags` | 태그 생성 |
| `POST` | `/tags/delete` | 태그 삭제 |
| `POST` | `/folders` | 폴더 생성 |
| `POST` | `/folders/:folder_id/trash` | 폴더 휴지통 이동 |
| `POST` | `/folders/:folder_id/restore` | 폴더 복구 |
| `POST` | `/folders/:folder_id/rename` | 폴더 이름 변경 |
| `POST` | `/folders/:folder_id/move` | 폴더 이동 |
| `POST` | `/job/:job_id/trash` | 작업 휴지통 이동 |
| `POST` | `/job/:job_id/restore` | 작업 복구 |
| `POST` | `/job/:job_id/rename` | 작업 이름 변경 |
| `POST` | `/job/:job_id/tags` | 작업 태그 변경 |
| `POST` | `/job/:job_id/refine` | 완료 작업 정제 재요청 |

이 그룹의 특징:

- 응답이 JSON이 아니라 redirect 중심이다.
- 신구 흐름을 함께 유지해야 할 때 regression 위험이 큰 지점이다.
- 신규 기능 추가는 가능하면 `/api/*` 쪽에 먼저 두는 편이 낫다.

## 5. JSON API

### 파일 / 폴더 / 탐색

| Method | Path | 설명 |
|---|---|---|
| `GET` | `/api/files` | 파일/폴더 목록, 홈/탐색/검색 뷰 조회 |
| `POST` | `/api/upload` | SPA 업로드 |
| `POST` | `/api/folders` | 폴더 생성 |
| `PATCH` | `/api/folders/:folder_id` | 폴더 이름 변경 |
| `DELETE` | `/api/folders/:folder_id` | 폴더 휴지통 이동 |
| `GET` | `/api/folders/:folder_id/download` | 폴더 하위 완료 결과 ZIP 다운로드 |
| `POST` | `/api/move` | 작업/폴더 batch move |

### 작업 상세 / 제어

| Method | Path | 설명 |
|---|---|---|
| `GET` | `/api/jobs/:job_id` | 작업 상세 조회 |
| `GET` | `/api/jobs/:job_id/audio` | AAC 오디오 스트리밍 |
| `GET` | `/api/jobs/:job_id/pdf` | 원본 PDF 반환 |
| `POST` | `/api/jobs/:job_id/retry` | 실패 작업 재시도 |
| `POST` | `/api/jobs/:job_id/retranscribe` | 완료 작업 재전사 |
| `POST` | `/api/jobs/:job_id/refine` | 정제 요청 |
| `POST` | `/api/jobs/:job_id/rerefine` | 정제본 삭제 후 재정제 |
| `PATCH` | `/api/jobs/:job_id` | 작업 이름 변경 |
| `DELETE` | `/api/jobs/:job_id` | 작업 휴지통 이동 |
| `POST` | `/api/jobs/:job_id/restore` | 작업 복구 |
| `PUT` | `/api/jobs/:job_id/tags` | 작업 태그 업데이트 |

### 태그

| Method | Path | 설명 |
|---|---|---|
| `GET` | `/api/tags` | 태그 목록 조회 |
| `POST` | `/api/tags` | 태그 생성 |
| `DELETE` | `/api/tags/:name` | 태그 삭제 |

### 휴지통

| Method | Path | 설명 |
|---|---|---|
| `GET` | `/api/trash` | 휴지통 목록 조회 |
| `POST` | `/api/trash/clear` | 휴지통 전체 비우기 |
| `POST` | `/api/trash/jobs/delete` | 선택 작업 영구 삭제 |
| `POST` | `/api/folders/:folder_id/restore` | 휴지통 폴더 복구 |

### 저장공간 / SSE

| Method | Path | 설명 |
|---|---|---|
| `GET` | `/api/storage` | 사용자 저장공간 집계 |
| `GET` | `/api/events` | SSE stream |

## 6. 운영용 엔드포인트

| Method | Path | 설명 |
|---|---|---|
| `GET` | `/healthz` | 프로세스 health check |
| `GET` | `/metrics` | Prometheus metrics |

## 7. 신규 개발자를 위한 참고

엔드포인트를 따라 코드를 읽을 때는 이 순서를 추천한다.

1. [`routes.go`](/Users/sh_kim/Library/Mobile Documents/com~apple~CloudDocs/workspace/whisperServer/src/internal/transport/http/routes.go)
2. 관심 있는 handler 파일
3. [`http_handlers.go`](/Users/sh_kim/Library/Mobile Documents/com~apple~CloudDocs/workspace/whisperServer/src/internal/server/http_handlers.go)
4. 관련 `service`
5. 필요 시 `runtime`, `repo/sqlite`, `worker`

예를 들어:

- `/api/files` -> `handlers_api_files.go` -> `query/files/query.go` -> `service/folder_service.go`
- `/api/jobs/:job_id` -> `handlers_api_job_detail.go` -> `runtime/runtime.go` + `service/job_blob_service.go`
- `/api/upload` -> `handlers_api_upload.go` -> `service/upload_service.go` -> `worker/worker.go`
