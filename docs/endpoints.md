# Endpoints

이 문서는 현재 [`src/internal/app/run.go`](/Users/sh_kim/Library/Mobile%20Documents/com~apple~CloudDocs/workspace/whisperServer/src/internal/app/run.go)에 등록된 엔드포인트를 기준으로 정리했다.  
구분은 현재 프로젝트의 실제 동작 흐름에 맞춰 `인증`, `SPA 진입`, `레거시 폼/다운로드`, `JSON API`, `운영용 엔드포인트`로 나눴다.

기본 전제:
- 대부분의 파일/작업/태그/폴더 관련 엔드포인트는 인증이 필요하다.
- SPA 화면은 React 라우터가 담당하고, 서버는 진입 HTML만 제공하거나 구경로를 신경로로 리다이렉트한다.
- `/api/*`는 프론트엔드가 직접 호출하는 JSON API다.
- `/download/*`, `/jobs/updates`, `/status/*` 등은 레거시 서버 주도 흐름과 다운로드/폴링용 경로다.
- 최근 구조 정리로 태그와 업로드의 JSON 핸들러도 `internal/http` 공통 계층으로 올라가 있어, 레거시 폼 핸들러와 정책을 공유한다.

## 1. 인증 및 세션

| Method | Path | 인증 | 기능 |
|---|---|---|---|
| `GET` | `/login` | 불필요 | 로그인 화면 별칭. 현재는 `/auth/login`으로 보내는 진입점 역할 |
| `POST` | `/login` | 불필요 | 폼 로그인 처리, 성공 시 세션 쿠키 발급 |
| `GET` | `/signup` | 불필요 | 회원가입 화면 별칭. 현재는 `/auth/join`으로 보내는 진입점 역할 |
| `POST` | `/signup` | 불필요 | 회원가입 처리, 사용자 생성 후 세션 시작 |
| `POST` | `/logout` | 필요 | 세션 쿠키 삭제 후 로그아웃 |
| `GET` | `/auth/login` | 불필요 | React 로그인 페이지 SPA 진입점 |
| `GET` | `/auth/join` | 불필요 | React 회원가입 페이지 SPA 진입점 |
| `POST` | `/api/auth/signup` | 불필요 | JSON 회원가입 API |
| `POST` | `/api/auth/login` | 불필요 | JSON 로그인 API |
| `POST` | `/api/auth/logout` | 필요 | JSON 로그아웃 API |
| `GET` | `/api/me` | 필요 | 현재 로그인 사용자 정보 조회 |

### 상세

#### `GET /login`
- 목적: 오래된 로그인 링크를 현재 인증 화면 흐름으로 연결한다.
- 동작: 서버에서 직접 로그인 폼을 렌더링하지 않고, 인증 페이지로 이동시키는 진입점으로 사용된다.

#### `POST /login`
- 목적: 아이디 또는 이메일과 비밀번호로 로그인한다.
- 입력: 폼 필드 기반 인증 정보.
- 결과: 성공 시 JWT 기반 인증 쿠키를 발급하고 메인 파일 화면으로 복귀시킨다.

#### `GET /signup`
- 목적: 오래된 회원가입 링크 호환용 경로다.
- 동작: 회원가입 SPA 진입 경로를 가리킨다.

#### `POST /signup`
- 목적: 신규 사용자를 생성하고 즉시 로그인 상태로 만든다.
- 입력: `login_id`, `email`, `password`.
- 결과: 사용자 저장 후 인증 쿠키를 발급한다.

#### `POST /logout`
- 목적: 브라우저 세션을 종료한다.
- 결과: 인증 쿠키를 만료시키고 로그아웃 이후 화면으로 이동시킨다.

#### `GET /auth/login`
- 목적: React 기반 로그인 화면을 띄우는 SPA 엔트리다.
- 동작: 번들된 `index.html`을 반환한다.

#### `GET /auth/join`
- 목적: React 기반 회원가입 화면을 띄우는 SPA 엔트리다.
- 동작: 번들된 `index.html`을 반환한다.

#### `POST /api/auth/signup`
- 목적: SPA 회원가입 폼이 호출하는 JSON 엔드포인트다.
- 입력: JSON 본문 `login_id`, `email`, `password`.
- 결과: 성공 시 사용자 메타데이터와 함께 로그인된 상태를 만든다.

#### `POST /api/auth/login`
- 목적: SPA 로그인 폼이 호출하는 JSON 엔드포인트다.
- 입력: JSON 본문 `identifier`, `password`.
- 결과: 성공 시 인증 쿠키를 발급하고 사용자 정보를 반환한다.

#### `POST /api/auth/logout`
- 목적: SPA에서 로그아웃 버튼 클릭 시 세션을 끊는다.
- 결과: 쿠키를 제거하고 성공 상태를 JSON으로 반환한다.

#### `GET /api/me`
- 목적: 현재 로그인 사용자의 프로필 정보를 확인한다.
- 결과: `id`, `login_id`, `email`, `displayName`을 반환한다.
- 사용처: 앱 셸 초기화, 네비게이션 표시 이름 구성.

## 2. SPA 진입 및 구경로 호환

| Method | Path | 인증 | 기능 |
|---|---|---|---|
| `GET` | `/` | 상황별 | 로그인 여부에 따라 메인 파일 화면 또는 로그인 화면으로 리다이렉트 |
| `GET` | `/files` | 필요 | `/files/home`으로 보냄 |
| `GET` | `/files/home` | 필요 | 파일 홈 화면 SPA 진입점 |
| `GET` | `/files/root` | 필요 | 루트 폴더 탐색 SPA 진입점 |
| `GET` | `/files/folder/:folder_id` | 필요 | 특정 폴더 탐색 SPA 진입점 |
| `GET` | `/files/search` | 필요 | 검색 결과 SPA 진입점 |
| `GET` | `/files/trash` | 필요 | 휴지통 SPA 진입점 |
| `GET` | `/files/storage` | 필요 | 저장공간 SPA 진입점 |
| `GET` | `/file/:job_id` | 필요 | 작업 상세 SPA 진입점 |
| `GET` | `/app` | 필요 | SPA 번들 직접 진입용 별칭 |
| `GET` | `/app/*` | 필요 | SPA 하위 경로 별칭 |
| `GET` | `/files/folders/:folder_id` | 필요 | 과거 폴더 경로를 현재 `/files/folder/:folder_id`로 정리 |
| `GET` | `/job/:job_id` | 필요 | 과거 작업 상세 경로를 현재 `/file/:job_id`로 정리 |
| `GET` | `/upload` | 필요 | 예전 업로드 경로를 파일 탐색 루트로 연결 |
| `GET` | `/jobs` | 필요 | 예전 작업 목록 경로를 파일 홈으로 연결 |
| `GET` | `/trash` | 필요 | 구 휴지통 경로를 `/files/trash`로 연결 |
| `GET` | `/tags` | 필요 | 태그 전용 페이지 대신 파일 홈으로 연결 |

### 상세

#### `GET /`
- 목적: 최초 진입 시 기본 화면을 결정한다.
- 동작: 로그인 상태면 `/files/home`, 아니면 `/auth/login`으로 이동한다.

#### `GET /files`
- 목적: 메인 파일 관리 화면의 표준 시작점이다.
- 동작: 홈 탭인 `/files/home`으로 영구 이동시킨다.

#### `GET /files/home`
- 목적: 최근 작업 중심의 홈 화면을 연다.
- 특징: 폴더/작업 요약, 최근 파일, 빠른 진입을 위한 뷰다.

#### `GET /files/root`
- 목적: 루트 기준 전체 탐색 화면을 연다.
- 특징: 폴더 구조와 파일 목록을 함께 탐색한다.

#### `GET /files/folder/:folder_id`
- 목적: 특정 폴더 내부를 탐색한다.
- 입력: `folder_id`.
- 결과: SPA 진입 후 프론트가 `/api/files?folder_id=...`로 실데이터를 가져간다.

#### `GET /files/search`
- 목적: 검색 결과 전용 SPA 화면을 연다.
- 특징: 질의 문자열은 프론트가 API 호출 시 전달한다.

#### `GET /files/trash`
- 목적: 휴지통 관리 화면을 연다.

#### `GET /files/storage`
- 목적: 저장공간 사용량과 파일별 용량 화면을 연다.

#### `GET /file/:job_id`
- 목적: 단일 작업 상세 화면을 연다.
- 특징: 전사 결과, 정제 결과, 오디오 재생, 재시도/정제 액션이 이 화면에서 이뤄진다.

#### `GET /app`, `GET /app/*`
- 목적: SPA 정적 번들을 직접 여는 호환 경로다.
- 특징: 내부적으로 모두 SPA `index.html`을 반환한다.

#### `GET /files/folders/:folder_id`
- 목적: 과거 폴더 URL을 현재 URL 체계로 치환한다.
- 동작: 쿼리스트링을 유지한 채 `/files/folder/:folder_id` 또는 유사한 현대 경로로 리다이렉트한다.

#### `GET /job/:job_id`
- 목적: 구 작업 상세 URL을 현재 `/file/:job_id`로 정리한다.

#### `GET /upload`
- 목적: 과거 업로드 전용 화면 링크를 현재 파일 탐색 루트로 연결한다.

#### `GET /jobs`
- 목적: 과거 작업 목록 링크를 현재 홈 화면으로 연결한다.

#### `GET /trash`
- 목적: 과거 휴지통 링크를 현재 휴지통 페이지로 연결한다.

#### `GET /tags`
- 목적: 과거 태그 전용 링크를 현재 파일 홈으로 연결한다.

## 3. 레거시 폼/다운로드/실시간 상태

| Method | Path | 인증 | 기능 |
|---|---|---|---|
| `POST` | `/upload` | 필요 | 폼 업로드 처리, 작업 생성 후 큐 등록 |
| `GET` | `/jobs/updates` | 필요 | 작업 목록 업데이트용 실시간/폴링 데이터 |
| `GET` | `/status/:job_id` | 필요 | 단일 작업 상태 조회 |
| `GET` | `/download/:job_id` | 필요 | 전사 텍스트 다운로드 |
| `GET` | `/download/:job_id/refined` | 필요 | 정제 텍스트 다운로드 |
| `POST` | `/batch-download` | 필요 | 여러 작업 결과를 묶어 다운로드 |
| `POST` | `/batch-delete` | 필요 | 여러 작업 일괄 삭제 |
| `POST` | `/batch-move` | 필요 | 여러 작업/폴더를 다른 폴더로 이동 |
| `POST` | `/tags` | 필요 | 태그 생성 |
| `POST` | `/tags/delete` | 필요 | 태그 삭제 |
| `POST` | `/folders` | 필요 | 폴더 생성 |
| `POST` | `/folders/:folder_id/trash` | 필요 | 폴더를 휴지통으로 이동 |
| `POST` | `/folders/:folder_id/restore` | 필요 | 휴지통 폴더 복구 |
| `POST` | `/folders/:folder_id/rename` | 필요 | 폴더 이름 변경 |
| `POST` | `/folders/:folder_id/move` | 필요 | 폴더 이동 |
| `POST` | `/job/:job_id/trash` | 필요 | 작업을 휴지통으로 이동 |
| `POST` | `/job/:job_id/restore` | 필요 | 휴지통 작업 복구 |
| `POST` | `/job/:job_id/rename` | 필요 | 작업명 변경 |
| `POST` | `/job/:job_id/tags` | 필요 | 작업 태그 변경 |
| `POST` | `/job/:job_id/refine` | 필요 | 완료된 전사를 정제 큐에 다시 등록 |

### 상세

#### `POST /upload`
- 목적: 멀티파트 폼 업로드를 처리한다.
- 입력: 파일, 표시 이름, 설명, 태그, 폴더, 정제 여부.
- 결과: AAC 오디오 저장, 작업 레코드 생성, 전사 큐 등록.

#### `GET /jobs/updates`
- 목적: 작업 목록 화면에서 상태 변화를 받아오는 용도다.
- 특징: 변경 여부와 최신 목록 조각을 내려준다.

#### `GET /status/:job_id`
- 목적: 단일 작업의 상태와 진행률만 간단히 확인한다.

#### `GET /download/:job_id`
- 목적: 원본 전사 텍스트를 파일로 다운로드한다.

#### `GET /download/:job_id/refined`
- 목적: 정제된 텍스트를 파일로 다운로드한다.

#### `POST /batch-download`
- 목적: 여러 작업 결과를 한 번에 내려받는다.
- 결과: 일반적으로 압축 파일 형태로 응답한다.

#### `POST /batch-delete`
- 목적: 선택한 작업들을 영구 삭제한다.

#### `POST /batch-move`
- 목적: 선택한 작업/폴더를 지정한 폴더로 이동한다.

#### `POST /tags`
- 목적: 새 태그를 생성한다.
- 입력: 태그명과 설명.

#### `POST /tags/delete`
- 목적: 태그를 삭제하고 관련 작업에서 태그를 제거한다.

#### `POST /folders`
- 목적: 폴더를 생성한다.

#### `POST /folders/:folder_id/trash`
- 목적: 폴더 전체를 휴지통 상태로 바꾼다.
- 특징: 하위 폴더/작업에도 동일 상태를 전파한다.

#### `POST /folders/:folder_id/restore`
- 목적: 휴지통 폴더를 복구한다.

#### `POST /folders/:folder_id/rename`
- 목적: 폴더 이름을 바꾼다.

#### `POST /folders/:folder_id/move`
- 목적: 폴더의 부모 폴더를 변경한다.
- 특징: 자기 하위 폴더 안으로 이동하는 잘못된 구조를 막는다.

#### `POST /job/:job_id/trash`
- 목적: 작업을 휴지통으로 보낸다.

#### `POST /job/:job_id/restore`
- 목적: 휴지통 작업을 복구한다.
- 특징: 필요한 경우 전사/정제 작업을 다시 큐에 넣는다.

#### `POST /job/:job_id/rename`
- 목적: 파일 표시 이름을 바꾼다.

#### `POST /job/:job_id/tags`
- 목적: 작업에 연결된 태그 집합을 바꾼다.

#### `POST /job/:job_id/refine`
- 목적: 이미 전사 완료된 결과를 다시 정제 요청한다.
- 조건: Gemini 설정과 원본 전사 결과가 있어야 한다.

## 4. JSON API: 파일 탐색, 업로드, 작업 상세

| Method | Path | 인증 | 기능 |
|---|---|---|---|
| `GET` | `/api/files` | 필요 | 파일/폴더 목록 조회 |
| `POST` | `/api/upload` | 필요 | SPA 업로드 처리 |
| `GET` | `/api/jobs/:job_id` | 필요 | 작업 상세 데이터 조회 |
| `GET` | `/api/jobs/:job_id/audio` | 필요 | 업로드 오디오 스트리밍 |
| `POST` | `/api/jobs/:job_id/retry` | 필요 | 실패한 전사 재시도 |
| `POST` | `/api/jobs/:job_id/retranscribe` | 필요 | 완료된 작업의 전사/정제 결과를 지우고 전사를 다시 시작 |
| `POST` | `/api/jobs/:job_id/refine` | 필요 | 완료된 전사 정제 요청 |
| `POST` | `/api/jobs/:job_id/rerefine` | 필요 | 기존 정제본을 지우고 정제를 다시 시작 |
| `PATCH` | `/api/jobs/:job_id` | 필요 | 작업명 변경 |
| `DELETE` | `/api/jobs/:job_id` | 필요 | 작업 휴지통 이동 |
| `PUT` | `/api/jobs/:job_id/tags` | 필요 | 작업 태그 덮어쓰기 |
| `POST` | `/api/jobs/:job_id/restore` | 필요 | 휴지통 작업 복구 |

### 상세

#### `GET /api/files`
- 목적: 파일 탐색/검색/홈 화면의 주 데이터 소스다.
- 주요 쿼리:
  - `view`: `home`, `explore`, `search`
  - `q`: 검색어
  - `tag`: 태그 필터
  - `folder_id`: 현재 폴더
  - `sort`, `order`
  - `page`, `page_size`
  - `v`: 이전 스냅샷 버전
- 결과:
  - 폴더 목록, 작업 목록, 폴더 경로, 태그 목록, 전체 폴더 목록, 페이지네이션 정보, 버전 문자열.
  - 버전이 변하지 않았으면 `changed: false`만 내려 불필요한 재렌더링을 줄인다.

#### `POST /api/upload`
- 목적: SPA 업로드 폼과 드래그앤드롭 업로드를 처리한다.
- 입력:
  - 멀티파트 `file`
  - `display_name`
  - `description`
  - `tag` 또는 다중 태그
  - `folder_id`
  - `refine`
- 결과: 작업 ID, 파일명, 작업 상세 URL.

#### `GET /api/jobs/:job_id`
- 목적: 작업 상세 페이지에 필요한 모든 정보를 반환한다.
- 반환 내용:
  - 기본 작업 메타데이터
  - 현재 상태
  - 태그 목록과 사용 가능 태그
  - 오디오 URL
  - 결과 텍스트 또는 진행 중 미리보기
  - 정제 결과 존재 여부와 다운로드 URL
- 특징: 완료/정제 중/대기 중 상태에 따라 응답 모양이 일부 달라진다.

#### `GET /api/jobs/:job_id/audio`
- 목적: 업로드된 오디오를 브라우저에서 재생할 수 있게 한다.
- 결과: `audio/mp4` 스트리밍 응답.

#### `POST /api/jobs/:job_id/retry`
- 목적: 실패 상태 작업을 전사 큐에 다시 넣는다.
- 조건: 실패 상태이고 오디오 blob이 남아 있어야 한다.

#### `POST /api/jobs/:job_id/retranscribe`
- 목적: 완료된 작업의 전사 결과와 정제 결과를 삭제한 뒤 전사를 처음부터 다시 수행한다.
- 조건: 완료 상태이고 오디오 blob이 남아 있어야 한다.
- 특징: 기존에 정제 결과 blob이 있었다면 `refine_enabled`를 다시 켜서 전사 완료 후 정제도 자동으로 다시 진행한다.

#### `POST /api/jobs/:job_id/refine`
- 목적: 전사 완료 결과를 정제 큐에 넣는다.
- 조건: 완료 상태, Gemini 설정 존재, 원본 전사 존재, 아직 정제되지 않음.

#### `POST /api/jobs/:job_id/rerefine`
- 목적: 이미 정제된 완료 작업의 정제 결과를 지우고 다시 정제한다.
- 조건: 완료 상태, Gemini 설정 존재, 원본 전사 존재, 기존 정제 blob 존재.
- 특징: transcript는 유지하고 refined blob과 표시 상태만 지운 뒤 정제 큐에 다시 넣는다.

#### `PATCH /api/jobs/:job_id`
- 목적: 작업 표시 이름을 변경한다.
- 검증: 비어 있는 이름과 경로 문자가 포함된 이름을 막는다.

#### `DELETE /api/jobs/:job_id`
- 목적: 작업을 휴지통으로 이동한다.

#### `PUT /api/jobs/:job_id/tags`
- 목적: 작업 태그 집합을 전체 교체한다.
- 특징: 소유자가 가진 태그 이름만 허용한다.

#### `POST /api/jobs/:job_id/restore`
- 목적: 휴지통 작업을 복구한다.
- 특징: 폴더가 사라졌거나 휴지통이면 폴더도 함께 복구하거나 임시 복구 폴더를 생성한다.

## 5. JSON API: 폴더/이동/다운로드

| Method | Path | 인증 | 기능 |
|---|---|---|---|
| `POST` | `/api/folders` | 필요 | 폴더 생성 |
| `PATCH` | `/api/folders/:folder_id` | 필요 | 폴더명 변경 |
| `DELETE` | `/api/folders/:folder_id` | 필요 | 폴더 휴지통 이동 |
| `POST` | `/api/folders/:folder_id/restore` | 필요 | 폴더 복구 |
| `GET` | `/api/folders/:folder_id/download` | 필요 | 폴더 내 완료 작업 결과 일괄 다운로드 |
| `POST` | `/api/move` | 필요 | 작업/폴더 배치 이동 |

### 상세

#### `POST /api/folders`
- 목적: 새 폴더를 만든다.
- 입력: `name`, `parent_id`.
- 검증: 부모 폴더 존재 여부, 휴지통 폴더 여부, 이름 중복.

#### `PATCH /api/folders/:folder_id`
- 목적: 폴더 이름을 바꾼다.

#### `DELETE /api/folders/:folder_id`
- 목적: 폴더와 하위 작업을 휴지통으로 보낸다.

#### `POST /api/folders/:folder_id/restore`
- 목적: 휴지통 폴더를 복구한다.

#### `GET /api/folders/:folder_id/download`
- 목적: 폴더 하위 완료 작업의 텍스트 결과를 ZIP으로 다운로드한다.
- 특징: 정제본이 있으면 정제본을 우선 포함한다.

#### `POST /api/move`
- 목적: 여러 작업과 폴더를 한 번에 다른 폴더로 이동한다.
- 입력: `job_ids`, `folder_ids`, `target_folder_id`.
- 검증: 대상 폴더 유효성, 순환 이동 방지, 휴지통 폴더 제외.

## 6. JSON API: 태그/저장공간/휴지통/실시간

| Method | Path | 인증 | 기능 |
|---|---|---|---|
| `GET` | `/api/tags` | 필요 | 내 태그 목록 조회 |
| `POST` | `/api/tags` | 필요 | 태그 생성 또는 설명 갱신 |
| `DELETE` | `/api/tags/:name` | 필요 | 태그 삭제 |
| `GET` | `/api/storage` | 필요 | 저장용량 대시보드 데이터 조회 |
| `GET` | `/api/trash` | 필요 | 휴지통 목록 조회 |
| `POST` | `/api/trash/clear` | 필요 | 휴지통 전체 비우기 |
| `POST` | `/api/trash/jobs/delete` | 필요 | 선택한 휴지통 작업 영구 삭제 |
| `GET` | `/api/events` | 필요 | SSE 기반 파일 변경 이벤트 스트림 |

### 상세

#### `GET /api/tags`
- 목적: 현재 사용자의 모든 태그와 설명을 가져온다.

#### `POST /api/tags`
- 목적: 새 태그를 만들거나 기존 태그 설명을 갱신한다.
- 검증: 태그명 형식과 설명 존재 여부.

#### `DELETE /api/tags/:name`
- 목적: 태그를 삭제한다.
- 부수 효과: 해당 사용자의 모든 작업에서 같은 태그를 제거한다.

#### `GET /api/storage`
- 목적: 저장공간 화면에 필요한 통계를 제공한다.
- 반환 내용:
  - 총 용량, 사용량, 남은 용량, 사용 비율
  - 파일별 용량 목록
- 특징: 폴더명/휴지통 여부를 사람이 읽기 쉬운 위치명으로 정리해 내려준다.

#### `GET /api/trash`
- 목적: 휴지통 화면 목록을 가져온다.
- 입력: 선택적 검색어 `q`.
- 결과: 휴지통 작업 목록과 휴지통 폴더 목록.

#### `POST /api/trash/clear`
- 목적: 현재 사용자의 휴지통을 모두 비운다.
- 결과: 삭제된 작업 수와 상태.

#### `POST /api/trash/jobs/delete`
- 목적: 선택한 휴지통 작업만 영구 삭제한다.
- 입력: `job_ids`.

#### `GET /api/events`
- 목적: 파일 목록/휴지통/폴더 변경을 SSE로 전달한다.
- 특징:
  - 접속 직후 `ready` 이벤트를 보낸다.
  - 이후 `files.changed` 같은 업데이트 이벤트를 브로드캐스트한다.
  - 주기적으로 ping 코멘트를 보내 연결을 유지한다.

## 7. 운영 및 진단

| Method | Path | 인증 | 기능 |
|---|---|---|---|
| `GET` | `/healthz` | 불필요 | 애플리케이션 헬스체크 |
| `GET` | `/metrics` | 불필요 | Prometheus 메트릭 노출 |

### 상세

#### `GET /healthz`
- 목적: 프로세스 생존 여부를 아주 가볍게 확인한다.
- 결과: 간단한 성공 응답.

#### `GET /metrics`
- 목적: 큐 길이, 작업 수, 소요 시간, 업로드 바이트 등 Prometheus 메트릭을 노출한다.
- 사용처: 서버 운영 모니터링과 성능 관찰.
