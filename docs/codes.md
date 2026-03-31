# Code Map

이 문서는 현재 프로젝트의 모든 Go 파일과 TypeScript/TSX 파일을 기능 기준으로 정리한 코드 맵이다.  
구성은 다음 두 단계로 되어 있다.

1. 패키지/폴더 트리별 한 줄 요약
2. 파일별 상세 설명

## 1. 트리별 요약

### Go

#### `src/cmd/server`

| 파일 | 한 줄 요약 |
|---|---|
| `src/cmd/server/main.go` | 서버 부트스트랩 진입점으로 `app.Run()`만 호출한다. |

#### `src/internal/app`

| 파일 | 한 줄 요약 |
|---|---|
| `src/internal/app/auth.go` | 앱 레벨 인증 초기화와 현재 사용자 조회, `/api/me` 응답을 담당한다. |
| `src/internal/app/config.go` | 설정 파일과 환경값을 읽어 런타임 전역 설정을 초기화한다. |
| `src/internal/app/events.go` | 사용자별 SSE 브로커와 `/api/events` 스트림 핸들러를 제공한다. |
| `src/internal/app/gemini.go` | Gemini 기반 전사 정제 호출, 응답 스키마 검증, 재시도 제어를 담당한다. |
| `src/internal/app/globals.go` | 상태 문자열, 전역 설정, 정제 시스템 프롬프트, 공용 타입 별칭을 모은다. |
| `src/internal/app/http_api_file_ops.go` | 파일/폴더 배치 이동과 폴더 ZIP 다운로드 API를 제공한다. |
| `src/internal/app/http_api_files.go` | 파일 목록 조회, 폴더 생성/이름변경/삭제, 작업 이름변경/삭제 API를 담당한다. |
| `src/internal/app/http_api_jobs.go` | 작업 상세, 오디오 스트리밍, 실패 재시도, 전사 다시하기, 정제 API를 담당한다. |
| `src/internal/app/http_api_storage.go` | 저장공간 집계와 파일별 사용량 목록 API를 제공한다. |
| `src/internal/app/http_api_trash.go` | 휴지통 조회, 복구, 비우기, 선택 삭제 API를 담당한다. |
| `src/internal/app/http_common.go` | SPA 진입/리다이렉트와 앱 전용 인증/알림 보조 함수를 모은다. |
| `src/internal/app/http_deps.go` | `internal/http`용 의존성 구조체와 목록/정렬 브리지 함수를 조립한다. |
| `src/internal/app/http_spa_test.go` | SPA 진입 핸들러가 빌드 결과를 반환하는지 검증한다. |
| `src/internal/app/process_log.go` | 처리 로그 파일 초기화와 공통 로깅 함수를 제공한다. |
| `src/internal/app/run.go` | 서버 전체 초기화, 라우팅 등록, 워커 시작/종료를 담당하는 핵심 엔트리다. |
| `src/internal/app/runtime.go` | 메모리상의 작업 런타임 상태와 임시 파일/폴더 서브트리 유틸을 관리한다. |
| `src/internal/app/storage.go` | 작업 스냅샷 저장, 큐 등록, 필드 업데이트, 파생값 계산을 담당한다. |
| `src/internal/app/text.go` | 결과 텍스트 렌더링과 타임라인 텍스트 파싱 유틸을 제공한다. |

#### `src/internal/http`

| 파일 | 한 줄 요약 |
|---|---|
| `src/internal/http/auth_core.go` | 인증 미들웨어와 HTML/JSON 로그인·회원가입·로그아웃 처리를 제공한다. |
| `src/internal/http/common.go` | HTTP 공용 헬퍼와 인증/폴더/태그/복구 정책 보조 함수를 제공한다. |
| `src/internal/http/folders.go` | 서버 주도 폴더 생성/이동/이름변경 핸들러를 담당한다. |
| `src/internal/http/jobs_deps.go` | 작업 화면과 다운로드 로직이 사용하는 의존성 타입을 정의한다. |
| `src/internal/http/jobs_download.go` | 단일/배치 다운로드 핸들러를 담당한다. |
| `src/internal/http/jobs_pages.go` | 레거시 작업 목록/상세/상태 페이지와 업데이트 응답을 담당한다. |
| `src/internal/http/jobs_refine.go` | 레거시 정제 재시도 핸들러를 담당한다. |
| `src/internal/http/jobs_support.go` | 작업/폴더 목록 행 생성, 정렬, 버전 계산 유틸을 담당한다. |
| `src/internal/http/mutation.go` | 레거시 일괄 삭제 핸들러를 담당한다. |
| `src/internal/http/tags.go` | 레거시와 JSON 태그/작업태그 핸들러를 함께 담당한다. |
| `src/internal/http/trash.go` | 레거시 휴지통 복구/이동/이름변경 핸들러를 담당한다. |
| `src/internal/http/types.go` | 작업 목록/폴더 목록/상세 화면용 전달 타입을 정의한다. |
| `src/internal/http/upload.go` | 레거시와 JSON 업로드 핸들러 및 공통 업로드 생성 로직을 담당한다. |

#### `src/internal/model`

| 파일 | 한 줄 요약 |
|---|---|
| `src/internal/model/folder.go` | 폴더 도메인 모델을 정의한다. |
| `src/internal/model/job.go` | 작업 도메인 모델과 복제/정제 여부 메서드를 정의한다. |
| `src/internal/model/job_status.go` | 작업 상태 코드와 이름 변환 함수를 제공한다. |
| `src/internal/model/tag.go` | 태그 도메인 모델을 정의한다. |
| `src/internal/model/user.go` | 사용자 레코드 모델을 정의한다. |

#### `src/internal/routes`

| 파일 | 한 줄 요약 |
|---|---|
| `src/internal/routes/routes.go` | 서버/프론트가 공유하는 주요 URL 상수를 제공한다. |

#### `src/internal/store`

| 파일 | 한 줄 요약 |
|---|---|
| `src/internal/store/db_blobs.go` | 오디오/전사/정제 결과 blob 저장소를 관리한다. |
| `src/internal/store/db_core.go` | DB 초기화, 스키마 보정, 마이그레이션, 공통 직렬화 유틸을 담당한다. |
| `src/internal/store/db_folders.go` | 폴더 CRUD, 트리 이동, 휴지통, 경로, 조상 갱신 쿼리를 담당한다. |
| `src/internal/store/db_jobs.go` | 작업 스냅샷의 로드/저장을 담당한다. |
| `src/internal/store/db_users_tags.go` | 사용자 인증 데이터와 태그 CRUD 쿼리를 담당한다. |

#### `src/internal/util`

| 파일 | 한 줄 요약 |
|---|---|
| `src/internal/util/base.go` | 형변환, 태그 검증, 문자열/슬라이스 보정, 디렉터리 준비 유틸을 제공한다. |
| `src/internal/util/media.go` | 업로드 저장, ffmpeg 변환, 미디어 길이 계산, 확장자 판별 유틸을 제공한다. |

#### `src/internal/view`

| 파일 | 한 줄 요약 |
|---|---|
| `src/internal/view/renderer.go` | Echo 템플릿 렌더러를 초기화하고 기본 레이아웃 적용 규칙을 정의한다. |

#### `src/internal/worker`

| 파일 | 한 줄 요약 |
|---|---|
| `src/internal/worker/worker.go` | 전사/정제 작업 큐, 상태 전이, 워커 루프를 담당한다. |
| `src/internal/worker/worker_whisper.go` | Whisper CLI 실행, 출력 파싱, 전사 산출물 생성 로직을 담당한다. |

### TypeScript / TSX

#### `frontend/src`

| 파일 | 한 줄 요약 |
|---|---|
| `frontend/src/AppShell.tsx` | 인증 이후 공통 레이아웃, 네비게이션, 전역 검색과 로그아웃을 담당한다. |
| `frontend/src/main.tsx` | React 앱 진입점과 브라우저 라우터를 정의한다. |
| `frontend/src/pages.tsx` | 현재 라우터에서 사용하지 않는 플레이스홀더 페이지 컴포넌트를 제공한다. |
| `frontend/src/usePageTitle.ts` | 화면별 문서 제목을 설정하는 훅이다. |

#### `frontend/src/features/auth`

| 파일 | 한 줄 요약 |
|---|---|
| `frontend/src/features/auth/AuthPage.tsx` | 로그인/회원가입 화면을 하나의 컴포넌트로 처리한다. |
| `frontend/src/features/auth/api.ts` | 인증 관련 API 호출 함수를 제공한다. |

#### `frontend/src/features/files`

| 파일 | 한 줄 요약 |
|---|---|
| `frontend/src/features/files/FilesPage.tsx` | 파일 탐색/검색/홈 화면의 대부분 UI와 상호작용을 담당한다. |
| `frontend/src/features/files/api.ts` | 파일 화면에서 쓰는 조회/업로드/이동/이름변경/삭제 API를 제공한다. |
| `frontend/src/features/files/filesPageDateUtils.ts` | 파일 목록의 날짜 필터 계산 유틸을 제공한다. |
| `frontend/src/features/files/filesPageTypes.ts` | 파일 화면 전용 UI 상태와 필터 타입을 정의한다. |
| `frontend/src/features/files/filesPageUtils.ts` | 파일 화면 정렬, 표시 문자열, 쿼리 업데이트, 드래그/선택 유틸을 제공한다. |
| `frontend/src/features/files/types.ts` | 파일 API 응답과 목록 엔티티 타입을 정의한다. |
| `frontend/src/features/files/uploadStore.ts` | 업로드 진행 상태를 외부 스토어로 관리한다. |

#### `frontend/src/features/jobs`

| 파일 | 한 줄 요약 |
|---|---|
| `frontend/src/features/jobs/JobDetailPage.tsx` | 작업 상세, 결과 보기, 오디오 재생, 재시도/정제 UI를 담당한다. |
| `frontend/src/features/jobs/api.ts` | 작업 상세/재시도/정제 API를 제공한다. |
| `frontend/src/features/jobs/types.ts` | 작업 상세 응답 타입을 정의한다. |

#### `frontend/src/features/storage`

| 파일 | 한 줄 요약 |
|---|---|
| `frontend/src/features/storage/StoragePage.tsx` | 저장공간 통계와 파일별 용량 목록 UI를 담당한다. |
| `frontend/src/features/storage/api.ts` | 저장공간 조회 API와 타입을 제공한다. |

#### `frontend/src/features/tags`

| 파일 | 한 줄 요약 |
|---|---|
| `frontend/src/features/tags/TagsPage.tsx` | 태그 목록, 생성, 삭제 UI를 담당한다. |
| `frontend/src/features/tags/api.ts` | 태그 조회/생성/삭제/작업 태그 갱신 API를 제공한다. |

#### `frontend/src/features/trash`

| 파일 | 한 줄 요약 |
|---|---|
| `frontend/src/features/trash/TrashPage.tsx` | 휴지통 목록, 복구, 선택 삭제, 전체 비우기 UI를 담당한다. |
| `frontend/src/features/trash/api.ts` | 휴지통 조회/복구/삭제 API를 제공한다. |
| `frontend/src/features/trash/trashUtils.ts` | 휴지통 정렬/날짜 필터/표시 문자열 유틸을 제공한다. |

#### `frontend`

| 파일 | 한 줄 요약 |
|---|---|
| `frontend/vite.config.ts` | Vite 빌드 설정과 개발 서버 구성을 정의한다. |

## 2. 파일별 상세 설명

### Go

#### `src/cmd/server/main.go`
- 프로세스 진입점이다.
- 실제 서버 초기화는 모두 `app.Run()`에 위임한다.

#### `src/internal/app/auth.go`
- 앱 시작 시 JWT 시크릿과 `httpx.Auth` 인스턴스를 준비한다.
- Echo 컨텍스트에서 현재 사용자 정보를 읽는 얇은 앱 레벨 브리지 역할을 한다.
- `/api/me` 응답은 이 파일에서 직접 구성한다.

#### `src/internal/app/config.go`
- TOML 기반 설정 파일을 로드하고 전역 설정 변수로 펼친다.
- 문자열, 정수, 불리언, 리스트 형식을 모두 다룬다.
- 포트, Whisper, Gemini, 업로드 제한, 경로 같은 런타임 핵심 설정이 여기서 정해진다.

#### `src/internal/app/events.go`
- 사용자 ID별 SSE 구독 채널을 관리하는 브로커를 가진다.
- 파일 목록 변경 시 `files.changed` 이벤트를 같은 사용자 세션들에 뿌린다.
- `/api/events`는 ping 유지와 연결 해제를 포함해 SSE 스트림 전체를 책임진다.

#### `src/internal/app/gemini.go`
- Gemini API 키 풀을 로드하고 라운드로빈에 가깝게 사용한다.
- 전사 결과 정제 프롬프트 호출, JSON 스키마 강제, 응답 정규화를 담당한다.
- 실패 시 재시도 가능 오류를 구분하고 키별 backoff를 관리한다.

#### `src/internal/app/globals.go`
- 작업 상태 이름, 파일/경로 전역값, 메트릭 객체 같은 공유 상수를 모은다.
- 현재 실제로 사용하는 정제 시스템 프롬프트의 단일 소스가 여기에 있다.
- `httpx` 전달 타입에 대한 별칭도 포함한다.

#### `src/internal/app/http_api_file_ops.go`
- `/api/move`에서 작업과 폴더를 한 번에 이동시키는 로직을 처리한다.
- 이동 후 영향을 받은 폴더 조상들의 `updated_at`를 갱신한다.
- `/api/folders/:folder_id/download`는 완료된 텍스트 결과를 ZIP으로 묶는다.

#### `src/internal/app/http_api_files.go`
- `/api/files`의 핵심 응답을 만든다.
- 홈/탐색/검색 뷰에 따라 다른 작업/폴더 목록을 구성하고 정렬과 페이지네이션을 적용한다.
- 폴더 생성/이름변경/휴지통 이동, 작업 이름변경/휴지통 이동도 함께 담당한다.

#### `src/internal/app/http_api_jobs.go`
- 작업 상세 페이지용 JSON 응답을 상태별로 조립한다.
- 오디오 blob 스트리밍, 실패 작업 재시도, 완료 작업 전사 다시하기, 정제 재요청을 제공한다.
- 태그 갱신은 더 이상 이 파일에 없고 `internal/http/tags.go`의 공통 JSON 핸들러를 사용한다.

#### `src/internal/app/http_api_storage.go`
- blob 저장소 사용량을 파일 단위로 집계해 저장공간 화면 데이터를 만든다.
- 폴더/휴지통 위치명을 사람이 읽기 쉽게 정리한다.
- 총 용량 대비 사용률과 잔여 용량 계산도 여기서 수행한다.

#### `src/internal/app/http_api_trash.go`
- 휴지통 목록과 휴지통 폴더 목록을 반환한다.
- 작업/폴더 복구 시 필요한 폴더 복구와 재큐잉 정책을 적용한다.
- 휴지통 전체 비우기와 선택 삭제도 담당한다.

#### `src/internal/app/http_common.go`
- SPA 진입점과 레거시 URL 리다이렉트를 모아 둔다.
- 현재 사용자 검증, 작업 소유권 확인, 파일 변경 이벤트 알림 같은 앱 런타임 밀착 보조 기능이 모여 있다.

#### `src/internal/app/http_deps.go`
- `internal/http` 레거시 핸들러가 사용할 `Deps` 구조체를 조립한다.
- 작업/폴더 행 생성, 정렬, 스냅샷 버전 계산 등도 여기서 `httpx`에 연결한다.
- 현재 앱 내부와 `internal/http` 공통 핸들러 사이의 접점 역할을 한다.

#### `src/internal/app/http_spa_test.go`
- SPA 빌드 산출물이 있을 때 `index.html`을 반환하는지 검증한다.
- 빌드 산출물이 없을 때는 `503 Service Unavailable`을 반환하는지 검증한다.

#### `src/internal/app/process_log.go`
- 처리 로그 파일 핸들을 열고 닫는다.
- 일반 로그와 오류 로그를 같은 포맷으로 남기는 공통 진입점이다.

#### `src/internal/app/run.go`
- 서버의 실제 composition root다.
- 설정 초기화, DB 초기화, 인증 초기화, 런타임 스냅샷 로드, 워커 시작, Echo 라우팅, 종료 처리까지 모두 연결한다.

#### `src/internal/app/runtime.go`
- 메모리 상의 `jobs` 맵과 뮤텍스를 관리한다.
- 임시 WAV 파일 경로 계산, 비활성 임시 파일 청소, 폴더 서브트리 수집, 하위 작업 휴지통 전파를 담당한다.

#### `src/internal/app/storage.go`
- 작업 스냅샷을 로드/저장하고, 개별 작업 필드를 부분 업데이트한다.
- 큐 등록, 취소, 대기 작업 재등록, 태그 제거, 미리보기 텍스트 갱신도 여기서 처리한다.
- `Job -> JobView` 변환과 상태/진행률 파생값 계산도 포함한다.

#### `src/internal/app/text.go`
- 결과 텍스트를 타임라인 포함 여부에 따라 HTML로 렌더링한다.
- 타임라인 문자열에서 시작 시각을 뽑는 유틸이 있다.

#### `src/internal/http/auth_core.go`
- JWT 쿠키 기반 인증의 핵심 구현이다.
- 미들웨어에서 현재 사용자 정보를 컨텍스트에 싣고, HTML/JSON 로그인/회원가입/로그아웃 처리를 모두 가진다.
- 비밀번호 검증, 해시 저장, 식별자 정규화도 포함한다.

#### `src/internal/http/common.go`
- 캐시 비활성화, 안전한 리턴 경로 보정, 정렬 파라미터 정규화 같은 HTTP 헬퍼를 제공한다.
- 현재 사용자 표시 이름 계산, JSON 인증 실패 응답, 작업/폴더 소유권 검사, 태그 검증, 복구 후 재큐잉, 건강 확인 핸들러도 포함한다.

#### `src/internal/http/folders.go`
- 서버 주도 HTML/폼 흐름에서 폴더 관련 변경을 처리한다.
- 폴더 생성, 작업 이동, 폴더 이름변경, 폴더 이동이 이 파일에 있다.

#### `src/internal/http/jobs_deps.go`
- 작업 페이지, 다운로드, 정제 핸들러가 의존하는 함수 타입 묶음을 정의한다.
- 실제 구현은 `app/http_deps.go`가 채운다.

#### `src/internal/http/jobs_download.go`
- 전사/정제 텍스트 다운로드와 배치 다운로드를 담당한다.
- blob 존재 여부를 확인하고 적절한 파일명과 콘텐츠 타입을 설정한다.

#### `src/internal/http/jobs_pages.go`
- 레거시 작업 목록/상세/휴지통 페이지와 상태 업데이트를 렌더링한다.
- 페이지 렌더링용 데이터 모델 조립이 많이 들어 있는 파일이다.

#### `src/internal/http/jobs_refine.go`
- 레거시 상세 페이지에서 정제 재요청을 처리하는 핸들러 하나를 가진다.

#### `src/internal/http/jobs_support.go`
- 작업/폴더 목록 행 생성, 최근 작업 구성, 정렬, 버전 해시 계산을 담당한다.
- 현재 SPA `/api/files`도 이 로직을 재사용한다.

#### `src/internal/http/mutation.go`
- 서버 주도 UI에서 선택 작업 영구 삭제를 처리한다.

#### `src/internal/http/tags.go`
- 서버 주도 태그 생성/삭제와 작업 태그 갱신을 담당한다.
- 동시에 SPA용 JSON 태그 목록/생성/삭제/작업 태그 갱신 핸들러도 제공한다.

#### `src/internal/http/trash.go`
- 서버 주도 휴지통 복구/삭제 흐름을 담당한다.
- 작업/폴더 복구 후 필요한 전사/정제 재큐잉 정책도 포함한다.

#### `src/internal/http/types.go`
- 작업 상세, 작업 목록, 폴더 목록 응답에서 공통으로 쓰는 DTO를 정의한다.

#### `src/internal/http/upload.go`
- 서버 렌더링 업로드 화면과 업로드 처리 로직을 담당한다.
- 동시에 SPA용 JSON 업로드 핸들러도 제공하며, 실제 업로드 생성 로직은 이 파일의 공통 함수 하나로 통합되어 있다.

#### `src/internal/model/folder.go`
- 폴더의 ID, 소유자, 이름, 부모, 휴지통 여부, 갱신 시각을 가진다.

#### `src/internal/model/job.go`
- 업로드 파일 하나의 전체 상태를 표현하는 핵심 모델이다.
- 전사/정제 상태, 파일명, 설명, 태그, 폴더, blob 메타데이터를 가진다.

#### `src/internal/model/job_status.go`
- 상태 코드와 한글 상태 문자열의 양방향 변환을 제공한다.

#### `src/internal/model/tag.go`
- 태그 이름과 설명을 담는 단순 모델이다.

#### `src/internal/model/user.go`
- DB에서 읽은 사용자 인증 레코드 구조체다.

#### `src/internal/routes/routes.go`
- 파일 홈, 루트, 작업 상세 등 자주 쓰는 경로를 상수/함수로 관리한다.

#### `src/internal/store/db_blobs.go`
- 오디오, 전사 텍스트, 정제 텍스트, JSON 전사 결과를 blob 테이블에 저장한다.
- 사용자별 저장공간 집계에 필요한 용량 계산도 제공한다.

#### `src/internal/store/db_core.go`
- SQLite 연결 초기화와 기본 스키마 생성을 담당한다.
- 레거시 스키마를 현재 구조로 끌어오는 정규화/마이그레이션 로직이 많다.
- 태그 JSON 직렬화/역직렬화 같은 공용 유틸도 포함한다.

#### `src/internal/store/db_folders.go`
- 폴더 생성, 목록, 전체 목록, 단건 조회, 이동, 이름변경, 휴지통, 경로 조회를 담당한다.
- 재귀 CTE로 하위 폴더 일괄 처리와 조상 `updated_at` 갱신을 수행한다.

#### `src/internal/store/db_jobs.go`
- 메모리 스냅샷 전체를 DB에 로드/저장한다.
- job payload 직렬화 포맷과 blob-less 상태 저장의 경계다.

#### `src/internal/store/db_users_tags.go`
- 사용자 생성과 식별자 기반 사용자 조회를 담당한다.
- 태그 생성/조회/설명 조회/삭제도 여기서 처리한다.

#### `src/internal/util/base.go`
- 느슨한 타입의 값을 문자열/숫자/불리언/슬라이스로 안전하게 바꾼다.
- 태그명 정규식 검증, 중복 제거, 디렉터리 생성 같은 범용 함수가 있다.

#### `src/internal/util/media.go`
- 파일 확장자로 미디어 종류를 판별한다.
- 업로드 스트림을 제한 용량/속도로 저장하고, ffmpeg로 AAC/WAV 변환한다.
- ffprobe를 이용해 길이를 계산하고 보기 좋은 시각 문자열로 바꾼다.

#### `src/internal/view/renderer.go`
- Go HTML 템플릿을 로드하고 Echo 렌더러로 연결한다.
- 일부 페이지에는 공통 베이스 레이아웃을 씌우고, 일부는 독립 템플릿으로 렌더링한다.

#### `src/internal/worker/worker.go`
- 전사와 정제 큐를 운영하는 백그라운드 워커 핵심이다.
- 작업 취소, 큐 길이 메트릭, 상태 전이, 결과 blob 저장, 오류 처리까지 담당한다.

#### `src/internal/worker/worker_whisper.go`
- Whisper CLI를 외부 프로세스로 실행한다.
- stdout/stderr를 읽으며 진행률을 해석하고, JSON 결과를 slim transcript JSON과 타임라인 텍스트로 재구성한다.

### TypeScript / TSX

#### `frontend/src/AppShell.tsx`
- 로그인 후 공통 레이아웃과 좌측 내비게이션, 상단 검색, 저장공간 표시를 담당한다.
- 현재 URL과 검색어를 동기화하고, 로그아웃과 페이지 이동을 연결한다.

#### `frontend/src/main.tsx`
- 브라우저 라우터의 실제 라우트 트리를 정의한다.
- 로그인/회원가입은 독립 페이지, 나머지는 `AppShell` 하위 레이아웃으로 둔다.

#### `frontend/src/pages.tsx`
- 개요/플레이스홀더용 간단한 UI 컴포넌트를 제공한다.
- 현재 `main.tsx` 라우터에서는 사용하지 않아 실질적으로 보조 또는 잔존 파일에 가깝다.

#### `frontend/src/usePageTitle.ts`
- 페이지 진입 시 `document.title`을 바꾸는 간단한 커스텀 훅이다.

#### `frontend/src/features/auth/AuthPage.tsx`
- 로그인과 회원가입 화면을 `mode` 하나로 통합 처리한다.
- 입력값 검증, 에러 메시지 표시, 성공 후 라우팅 이동을 담당한다.

#### `frontend/src/features/auth/api.ts`
- 인증 관련 fetch 호출을 감싼다.
- 쿠키 기반 인증이라 `credentials` 처리와 오류 메시지 추출이 중요하다.

#### `frontend/src/features/files/FilesPage.tsx`
- 프로젝트 프론트엔드에서 가장 큰 화면 파일이다.
- 파일/폴더 목록 렌더링, 검색, 정렬, 필터, 선택, 드래그앤드롭, 업로드 진행 표시, 폴더 생성/이름변경/삭제, 작업 삭제/이름변경, 배치 다운로드까지 대부분의 상호작용을 가진다.

#### `frontend/src/features/files/api.ts`
- 파일 화면이 쓰는 API 호출 모음이다.
- 목록 조회, 폴더 CRUD, 작업명 변경, 작업 삭제, 업로드, 상태 조회, 이동, 폴더 다운로드, 배치 다운로드를 포함한다.

#### `frontend/src/features/files/filesPageDateUtils.ts`
- 날짜 문자열을 비교 가능한 값으로 바꾸고, 날짜 필터 라벨과 판별 로직을 제공한다.

#### `frontend/src/features/files/filesPageTypes.ts`
- 파일 화면 내부의 UI 상태 타입을 정리한다.
- 드래그 상태, 업로드 상태, 메뉴 상태, 확인 다이얼로그, 날짜/정렬 옵션 정의가 모여 있다.

#### `frontend/src/features/files/filesPageUtils.ts`
- 파일 화면에서 재사용되는 표시/정렬/선택/드래그 유틸을 제공한다.
- URL 쿼리 갱신, 폴더명 표시, 바이트 포맷, 항목 정렬, 선택 박스 계산 등이 있다.

#### `frontend/src/features/files/types.ts`
- `/api/files` 응답과 작업/폴더 엔티티 타입을 정의한다.
- 백엔드 응답 구조를 프론트에서 안정적으로 소비하기 위한 기본 타입 층이다.

#### `frontend/src/features/files/uploadStore.ts`
- React 외부에 업로드 상태 저장소를 두고 `useSyncExternalStore`로 구독한다.
- 진행률 업로드와 서버 반영 후 로컬 pending 항목 정리를 담당한다.

#### `frontend/src/features/jobs/JobDetailPage.tsx`
- 단일 작업 상세 화면을 담당한다.
- 결과 상태별 렌더링, 오디오 플레이어 동기화, 타임라인 파싱, 정제 결과 문단 해석, 재시도/전사 다시하기/정제 액션 UI를 가진다.

#### `frontend/src/features/jobs/api.ts`
- 작업 상세 조회, 실패 작업 재시도, 전사 다시하기, 정제 재요청 호출을 제공한다.

#### `frontend/src/features/jobs/types.ts`
- 작업 상세 응답의 구조를 타입으로 정의한다.
- 태그, 오디오 URL, 결과 텍스트, 정제 가능 여부 등을 포함한다.

#### `frontend/src/features/storage/StoragePage.tsx`
- 저장공간 사용량 요약과 파일별 사용량 표를 렌더링한다.
- 타입/날짜 필터, 정렬, 선택 다운로드, 휴지통 이동 액션을 포함한다.

#### `frontend/src/features/storage/api.ts`
- 저장공간 집계 응답 타입과 조회 함수를 제공한다.

#### `frontend/src/features/tags/TagsPage.tsx`
- 태그 목록 조회, 새 태그 생성, 삭제 UI를 처리한다.
- 상대적으로 단순한 CRUD 화면이다.

#### `frontend/src/features/tags/api.ts`
- 태그 목록 조회, 생성, 삭제, 작업 태그 덮어쓰기 API를 제공한다.

#### `frontend/src/features/trash/TrashPage.tsx`
- 휴지통 목록을 렌더링하고 복구, 선택 삭제, 전체 비우기를 제공한다.
- 날짜 필터, 정렬, 선택 상태, 네트워크 오류 처리까지 포함한다.

#### `frontend/src/features/trash/api.ts`
- 휴지통 조회, 작업 복구, 선택 삭제, 전체 비우기 API를 제공한다.

#### `frontend/src/features/trash/trashUtils.ts`
- 휴지통 목록 전용 정렬/날짜 필터/표시 문자열 유틸을 제공한다.

#### `frontend/vite.config.ts`
- React 플러그인과 개발 서버 포트를 포함한 Vite 빌드 설정 파일이다.
