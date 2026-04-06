# Backend Architecture

기준 시각: 2026-04-06 (KST)

이 문서는 현재 코드 기준의 백엔드 구조 설명서다.
처음 레포지토리를 읽는 개발자가 "어디서 시작해야 하는지", "어떤 패키지가 어떤 책임을 가지는지"를 빠르게 파악하는 것을 목표로 한다.

## 1. 한눈에 보는 구조

실제 백엔드 진입점은 다음 순서로 이어진다.

1. `src/cmd/server/main.go`
2. `src/internal/server/run.go`
3. `src/internal/server/bootstrap.go`
4. `src/internal/server/bootstrap_services.go`
5. `src/internal/server/bootstrap_http.go`
6. `src/internal/transport/http/routes.go`

즉, 현재 구조에서 `internal/server`는 composition root이고, 실제 요청 처리와 비즈니스 로직은 다른 패키지로 분리되어 있다.

## 2. 현재 패키지 레이아웃

아래는 현재 실제로 존재하는 핵심 leaf package 기준 레이아웃이다.

```text
src/cmd/server

src/internal/auth
src/internal/config
src/internal/domain
src/internal/integrations/gemini
src/internal/integrations/whisper
src/internal/obs
src/internal/query/files
src/internal/queue
src/internal/repo/sqlite
src/internal/runtime
src/internal/server
src/internal/service
src/internal/transport/http
src/internal/util
src/internal/worker
```

## 3. 패키지별 책임

### `src/cmd/server`

- 프로세스 시작점이다.
- 실제 초기화는 `internal/server`에 위임한다.

### `src/internal/server`

- 서버 부팅과 조립을 담당한다.
- 설정 로드, DB 초기화, auth/runtime/service/worker 생성, Echo 라우트 등록을 수행한다.
- 가능한 한 "조립만" 하고 직접 비즈니스 로직을 들지 않는 것이 원칙이다.

### `src/internal/transport/http`

- HTTP 경계다.
- Echo handler와 route table이 여기에 있다.
- HTML form 엔드포인트, JSON API, SSE, SPA 진입/리다이렉트가 모두 여기서 정의된다.
- handler는 service/runtime/query로 주입된 함수만 호출해야 한다.

### `src/internal/auth`

- JWT 발급/검증, 인증 middleware, 로그인/회원가입/로그아웃 처리.
- HTML form 로그인과 JSON 로그인 둘 다 지원한다.
- 인증 쿠키 이름, claim 구조, 인증 실패 정책의 기준점이다.

### `src/internal/domain`

- 핵심 도메인 타입 정의.
- 현재는 `Job`, `Folder`, `Tag`, `User`, `JobStatus`가 여기에 있다.
- 다른 패키지는 상태 문자열/숫자를 직접 흩뿌리기보다 이 패키지 타입을 기준으로 맞춘다.

### `src/internal/service`

- 유스케이스 계층이다.
- 현재 핵심 서비스:
  - `UploadService`
  - `FolderService`
  - `TagService`
  - `StorageService`
  - `JobBlobService`
  - `JobLifecycle`
  - `job_restore.go`의 복구 보조 로직
- "요청을 처리하는 흐름"은 가능하면 이 계층에 모아야 한다.

### `src/internal/runtime`

- 프로세스 메모리 안에 유지되는 런타임 상태 계층이다.
- 현재 역할:
  - in-memory job snapshot
  - SSE broker
  - worker enqueue/requeue/cancel 브리지
  - 임시 wav 파일 정리
  - 폴더 subtree 계산과 작업 휴지통 전파
- DB 영속 상태와 분리된 "현재 실행 중 서버의 메모리 상태"가 필요할 때 이 계층을 본다.

### `src/internal/queue`

- 큐 추상화와 in-memory 구현.
- 현재 워커 내부 처리 큐를 직접 채널로 노출하지 않기 위한 경계다.

### `src/internal/worker`

- 백그라운드 작업 소비자다.
- 작업 종류:
  - 음성 전사
  - 전사 정제
  - PDF 문서 추출
- 외부 도구/외부 API는 직접 구현하지 않고 integration/service를 호출한다.

### `src/internal/integrations/gemini`

- Gemini 연동 전담 패키지.
- API key rotation, cooldown, retry, 문서 추출, 전사 정제 담당.
- 워커는 Gemini SDK 세부 구현을 몰라야 한다.

### `src/internal/integrations/whisper`

- `whisper-cli` 실행 어댑터.
- stdout/stderr 진행률 파싱과 transcript 결과 후처리 담당.
- 워커는 Whisper 실행 프로토콜 대신 이 패키지 인터페이스를 사용한다.

### `src/internal/repo/sqlite`

- SQLite + blob persistence 계층.
- 현재 저장소 책임:
  - jobs snapshot
  - users
  - tags
  - folders
  - blobs
- 직접 SQL을 다루는 코드는 이 패키지에 둔다.

### `src/internal/query/files`

- 파일/폴더 화면 조회 전용 query 계층이다.
- `/api/files`, 레거시 `/jobs/updates`, 휴지통/목록 화면에 필요한 row를 구성한다.
- "쓰기"가 아니라 "조회 조립"이 목적이다.

### `src/internal/obs`

- 운영 로그 관련 코드.
- 현재는 processing log 초기화와 쓰기 보조 성격이 강하다.

### `src/internal/util`

- 공용 유틸.
- 현재는 형변환, 태그 검증, 업로드 저장, ffmpeg/ffprobe/pdf 유틸이 섞여 있다.
- 장기적으로는 일부를 `tools` 성격으로 다시 분리할 여지가 있다.

### `src/internal/config`

- 환경/설정 파일 값 파싱 보조.
- 실제 bootstrap에서 읽히는 런타임 설정 해석 로직의 바닥 계층이다.

## 4. 요청 처리 흐름

### 업로드

1. `transport/http`의 업로드 handler가 요청을 받는다.
2. `service.UploadService`가 파일 검증, 저장, blob 생성, job 생성까지 처리한다.
3. `runtime`이 in-memory snapshot에 job을 반영한다.
4. `worker`가 transcribe 또는 pdf extract 작업을 소비한다.
5. 결과는 `repo/sqlite` blob/jobs에 저장된다.
6. `runtime.Broker`가 SSE 이벤트를 발행한다.

### 작업 상세 조회

1. `transport/http`가 인증 후 job id를 읽는다.
2. `runtime.GetJob()`으로 현재 snapshot을 조회한다.
3. 필요 시 `service.JobBlobService`를 통해 transcript/refined/pdf blob을 읽는다.
4. 응답 DTO는 `transport/http`에서 최종 JSON으로 조립한다.

### 폴더/태그/휴지통 변경

1. `transport/http` handler가 입력을 해석한다.
2. `service.FolderService` 또는 `service.TagService`가 규칙을 적용한다.
3. 필요 시 `runtime`이 subtree/job snapshot을 갱신한다.
4. 변경 후 SSE로 파일 목록 변경 이벤트를 알린다.

## 5. 의존성 규칙

현재 유지해야 할 규칙은 아래와 같다.

- `server` -> `auth`, `runtime`, `service`, `worker`, `transport/http`, `integrations/*`, `repo/sqlite`
- `transport/http` -> `service`, `runtime`, `query/files`, `auth` DTO 수준
- `worker` -> `service`, `integrations`, `util`
- `service` -> `repo/sqlite`, `domain`, `util`
- `integrations/*` 는 `transport/http`를 import하면 안 된다
- `repo/sqlite`는 HTTP를 몰라야 한다

## 6. 처음 읽을 때 추천 순서

처음 레포를 보는 개발자라면 이 순서가 가장 빠르다.

1. `src/cmd/server/main.go`
2. `src/internal/server/bootstrap.go`
3. `src/internal/server/bootstrap_services.go`
4. `src/internal/transport/http/routes.go`
5. 관심 있는 기능의 handler
   - 파일 목록: `handlers_api_files.go`
   - 작업 상세: `handlers_api_job_detail.go`
   - 업로드: `handlers_api_upload.go`
   - 휴지통: `handlers_api_trash.go`
6. 대응 service
7. 필요 시 `runtime`과 `repo/sqlite`
8. 백그라운드 처리면 `worker`와 `integrations/*`

## 7. 현재 상태 요약

- 과거 `internal/app`, `internal/http`, `internal/model`, `internal/store` 중심 구조는 해체됐다.
- 현재는 `server / transport / auth / service / runtime / worker / integrations / repo` 경계가 명확해졌다.
- dead code 정리 후 `go test ./...`, `staticcheck ./...`, `deadcode ./...` 기준으로 통과 상태다.
- 다만 `util`에는 아직 도구성 코드가 남아 있으므로, 필요하면 이후 `tools` 성격으로 추가 분리할 수 있다.
