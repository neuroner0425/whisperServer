# Backend Architecture

기준 시각: 2026-04-06 (KST)

이 문서는 현재 `whisperServer` 백엔드의 실제 구조를 기준으로 유지한다.
이전 `codes.md`, `endpoints.md`는 더 이상 기준 문서가 아니다.

## Goals

- HTTP, 인증, 서비스, 런타임 상태, 워커, 외부 연동의 경계를 분리한다.
- 부트스트랩 패키지는 조립만 담당하고, 처리 로직은 별도 모듈로 이동한다.
- 외부 API/CLI/파일 변환은 integration 또는 utility 모듈 뒤로 숨긴다.
- 전역 상태는 최소화하고, 가능하면 runtime/service 객체를 통해 접근한다.

## Current Package Layout

```text
src/cmd/server

src/internal/auth
src/internal/config
src/internal/events
src/internal/format/markdown
src/internal/integrations/gemini
src/internal/integrations/whisper
src/internal/obs
src/internal/query/files
src/internal/queue
src/internal/runtime
src/internal/server
src/internal/service
src/internal/state
src/internal/store
src/internal/transport/http
src/internal/util
src/internal/view
src/internal/worker
```

## Responsibilities

1. `internal/server`
   - 프로세스 bootstrap, Echo 서버 생성, 서비스/워커/runtime/auth 조립
   - route wiring과 handler/deps builder 보유
   - 비즈니스 처리 로직은 직접 들지 않는다

2. `internal/transport/http`
   - Echo handler 구현과 route table
   - 입력 검증, 응답 포맷, SSE endpoint
   - service/runtime/query에서 주입받은 함수나 객체만 호출

3. `internal/auth`
   - JWT 발급/검증, 인증 middleware, 로그인/회원가입 handler

4. `internal/service`
   - 업로드, 폴더, 태그, blob, storage, job lifecycle 등 흐름/유스케이스

5. `internal/runtime`
   - in-memory job snapshot wrapper
   - SSE broker
   - worker enqueue/cancel/requeue
   - temp wav cleanup, subtree trash helper

6. `internal/query/files`
   - 파일/폴더 목록 조회 row builder
   - `transport/http` DTO를 직접 생성

7. `internal/worker`
   - queue consumer
   - 전사/정제/PDF 추출 파이프라인 orchestration
   - integration/service를 주입받아 호출

8. `internal/integrations/gemini`
   - Gemini client 생성
   - key rotation, cooldown, retry
   - transcript refine, document extraction/merge/render

9. `internal/integrations/whisper`
   - whisper-cli 실행
   - stdout/stderr 진행률 파싱
   - transcript output 후처리

10. `internal/store`
   - SQLite/Blob persistence

11. `internal/queue`
   - queue abstraction + in-memory implementation

12. `internal/format/markdown`
   - markdown/result text HTML 렌더링

## Dependency Rules

- `server` -> `auth`, `runtime`, `service`, `worker`, `transport/http`, `integrations/*`, `store`
- `transport/http` -> service/runtime/query/auth DTO 수준
- `worker` -> service + integrations
- `service` -> store/events/util 수준
- `integrations/*` 는 `transport/http` 를 몰라야 한다

## Current State Summary

- `internal/app` 는 비어 있으며 서버 엔트리포인트는 `internal/server`로 고정됐다.
- `internal/http` 레거시 패키지는 제거됐다.
- Gemini/Whisper 외부 연동은 모두 `internal/integrations/*` 아래로 이동했다.
- 파일/폴더 조회 빌더는 `internal/query/files`로 이동했다.
- markdown 렌더링은 `internal/format/markdown`으로 이동했다.
- `internal/server`는 거의 bootstrap 전용 패키지로 정리됐다.

## Remaining Direction

- 현재 구조는 목표 구조에 거의 도달했다.
- 이후 작업은 큰 모듈 분리보다 테스트 보강, 문서 유지, bootstrap 세부 정리 성격이 강하다.
