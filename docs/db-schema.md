# DB Schema

기준 시각: 2026-04-07 (KST)

이 문서는 현재 SQLite 기준의 canonical schema를 간단히 정리한 문서다.
상세 구현과 migration 순서는 `src/internal/repo/sqlite` 패키지를 기준으로 본다.

## 1. 저장 원칙

현재 저장 구조는 세 층으로 나뉜다.

- 관계형 메타데이터: jobs, users, folders, tags, job_tags
- 영구 결과 데이터: job_json
- 원본 바이너리: job_blobs

추가로 preview, PDF chunk 같은 runtime artifact는 DB가 아니라 `.run/job_runtime` 아래 파일로 둔다.

## 2. 테이블 개요

### `status_codes`

- 작업 상태 코드 마스터
- `code`를 PK로 사용한다
- 현재 앱 전반의 canonical status code 기준점이다

### `users`

- 사용자 계정 정보
- `id`가 PK다
- `email`은 unique
- `login_id`는 별도 unique index로 관리한다

### `folders`

- 사용자 폴더 구조
- `id`가 PK다
- `owner_id -> users(id)`
- `parent_id -> folders(id)`
- 루트 폴더는 `parent_id = NULL`

### `tags`

- 사용자별 태그 정의
- `id`가 PK다
- `owner_id -> users(id)`
- `(owner_id, name)` unique

### `jobs`

- 업로드된 작업의 메타데이터와 상태
- `id`가 PK다
- `status_code -> status_codes(code)`
- `owner_id -> users(id)`
- `folder_id -> folders(id)`

주요 컬럼:

- 파일명/파일 타입
- 업로드 시각
- 설명
- 정제 여부
- 휴지통 여부
- 진행률
- 시작/완료 시각

태그는 더 이상 `jobs` 컬럼에 JSON으로 저장하지 않고 `job_tags`로 분리한다.

### `job_tags`

- job과 tag의 연결 테이블
- `(job_id, tag_id)` PK
- `job_id -> jobs(id)`
- `tag_id -> tags(id)`
- `position`으로 태그 순서를 보존한다

### `job_json`

- job에 속한 JSON 결과 저장소
- `(job_id, kind)` PK
- `job_id -> jobs(id)`

현재 대표 kind:

- `transcript_json`
- `refined_timeline`
- `refined`
- `document_json`

### `job_blobs`

- job에 속한 영구 바이너리 저장소
- `(job_id, kind)` PK
- `job_id -> jobs(id)`

현재 주 용도는 업로드 원본 저장이다.

대표 kind:

- `audio_aac`
- `pdf_original`

## 3. 인덱스

현재 주요 인덱스는 다음과 같다.

- `idx_users_login_id`
- `idx_jobs_owner_trashed_uploaded`
- `idx_jobs_owner_folder_trashed`
- `idx_folders_owner_parent_trashed`
- `uq_folders_owner_parent_name`

이 인덱스들은 현재 파일 목록, 폴더 탐색, 휴지통 조회 패턴을 기준으로 둔다.

## 4. Migration 원칙

초기화 시 migration은 단계적으로 적용한다.

1. base schema 생성
2. legacy jobs 정규화
3. 관계형 FK 보강
4. artifact table 보장
5. legacy repair
6. runtime artifact 파일시스템 이관
7. one-time maintenance

즉 런타임 요청 처리 중에 schema를 추론하기보다, 앱 시작 시 DB를 현재 canonical 구조로 맞춘 뒤 그 상태를 사용한다.
