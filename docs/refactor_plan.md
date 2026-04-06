# Refactor Implementation Plan

기준 시각: 2026-04-06 (KST)

이 문서는 리팩터링 구현 진행 내역과 남은 성격의 작업을 기록한다.
현재 큰 구조 분리는 사실상 완료 상태다.

## Completed

### Server / Bootstrap

- `cmd/server` 진입점이 `internal/server`를 직접 호출하도록 정리
- `internal/app`의 bootstrap 구현을 `internal/server`로 이동
- `internal/server`가 실제 lifecycle owner가 됨
- `bootstrap_http.go`, `bootstrap_services.go`, `http_builders.go`, `http_handlers.go`로 조립 코드 분리

### HTTP / Auth

- route table과 handler 구현을 `internal/transport/http`로 이동
- 레거시 `internal/http` 패키지 제거
- 인증 코어와 runtime을 `internal/auth`로 이동
- 인증 회귀 테스트 추가

### Service / Worker / Queue

- `JobBlobService`, `FolderService`, `TagService`, `UploadService`, `StorageService`, `JobLifecycle` 도입
- `worker`의 blob I/O 직접 접근 제거
- `internal/queue` 도입 및 in-memory queue 구현 추가

### Integrations

- Gemini 연동을 `internal/integrations/gemini`로 이동
- Whisper CLI 실행을 `internal/integrations/whisper`로 이동
- `worker`는 더 이상 Gemini client 생성이나 `exec.Command` 기반 whisper 실행을 직접 수행하지 않음

### Runtime / Query / Formatting

- `internal/runtime` 도입
  - in-memory state wrapper
  - SSE broker
  - worker enqueue/cancel/requeue
  - temp wav cleanup
  - subtree trash helper
- 파일/폴더 조회 row builder를 `internal/query/files`로 이동

### Cleanup

- `internal/app` 비움
- `internal/model`을 `internal/domain`으로 정리
- `internal/store`를 `internal/repo/sqlite`로 정리
- `internal/events`, `internal/state`를 `internal/runtime`으로 흡수
- `internal/view`를 `internal/server`로 흡수
- `internal/routes`를 `internal/transport/http`로 흡수
- `internal/server/storage.go`, `internal/server/http_common.go`, `internal/server/job_rows.go`, `internal/server/transport_rows_adapter.go` 제거
- `internal/server/globals.go`를 `status.go`, `paths.go`, `metrics.go`, `types.go`로 분리
- 미사용 코드 제거
  - auth GET page handlers
  - markdown formatter 패키지
  - preview sanitizer
  - folder owner delete helper
  - SPA alias/query helper
  - secure filename helper
  - 사용되지 않던 server wrapper 함수/regex

### Tests Added

- `internal/auth/core_test.go`
- `internal/integrations/gemini/document_test.go`
- `internal/integrations/whisper/runtime_test.go`
- `internal/runtime/runtime_test.go`
- `internal/query/files/query_test.go`

## Current End State

현재 `internal/server`는 거의 bootstrap 전용 패키지다.
남아 있는 것은 대체로 다음뿐이다.

- 서버 시작/종료 lifecycle
- config/auth/runtime/service/worker 조립
- HTTP handler builder/wiring
- process log, metrics, paths/status constants

## Remaining Work

구조 리팩터링 기준으로는 남은 필수 작업이 없다.
이후 작업은 선택적 유지보수 성격이다.

1. 테스트 확대
   - `transport/http` route/handler smoke test 보강
   - `runtime`과 `worker` 통합 시나리오 보강

2. 문서 유지
   - 현재 구조가 바뀔 때 `docs/architecture.md`를 함께 갱신

3. 선택적 추가 정리
   - `internal/util` 중 외부 도구 실행 성격이 강한 코드를 `internal/tools`로 옮길지 검토

## Verification

- 현재 기준 `go test ./...` 통과
