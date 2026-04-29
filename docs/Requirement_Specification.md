# 추가 요구사항 구체화 명세서

기준 프로젝트 상태: 2026-04-29

이 문서는 기존 추가 요구사항을 현재 `whisperServer` 구조에 맞춰 구현 가능한 수준으로 구체화한 것이다. 현재 프로젝트는 Go 백엔드(`src/internal/*`)와 React SPA 프론트엔드(`frontend/src/*`)로 구성되어 있으며, 파일/폴더 화면은 `/api/files`, 업로드는 `/api/upload`, 작업 상세는 `/api/jobs/:job_id`, 작업 처리는 `worker`와 `integrations/*`가 담당한다.

## 1. 전제 및 현재 구조

### 1.1. 주요 화면

- 파일 목록/홈/검색/폴더 탐색: `frontend/src/features/files/FilesPage.tsx`
- 파일 업로드 상태 관리: `frontend/src/features/files/uploadStore.ts`
- 작업 상세/결과 보기: `frontend/src/features/jobs/JobDetailPage.tsx`
- 저장공간/휴지통/태그: `frontend/src/features/storage`, `frontend/src/features/trash`, `frontend/src/features/tags`

### 1.2. 주요 백엔드 흐름

- 라우트 등록: `src/internal/transport/http/routes.go`
- 파일 목록 API: `src/internal/transport/http/handlers_api_files.go`, `src/internal/query/files/query.go`
- 업로드 API: `src/internal/transport/http/handlers_api_upload.go`, `src/internal/service/upload_service.go`
- 작업 상세/제어 API: `src/internal/transport/http/handlers_api_job_detail.go`, `src/internal/transport/http/handlers_api_job_control.go`
- 작업 처리: `src/internal/worker/worker.go`, `src/internal/worker/worker_pdf.go`
- Gemini 연동: `src/internal/integrations/gemini/*`
- 결과 저장: SQLite `job_json`, `job_blobs`

### 1.3. 용어

- 작업: 업로드된 오디오 또는 PDF 단위의 처리 대상. DB/런타임에서는 `job`으로 표현한다.
- 원본 전사: Whisper가 만든 `transcript_json`.
- 정제본: Gemini가 원본 전사를 가공한 결과. 현재는 `refined` JSON 하나로 저장된다.
- 문서 JSON: PDF에서 추출한 `document_json`.
- 홈: `/files/home`. 폴더 위치와 무관하게 최근 작업을 보여주는 화면.
- 내 파일: `/files/root` 및 `/files/folder/:folder_id`. 폴더 탐색 화면.

## 2. UI/UX 요구사항

### 2.1. 다중 업로드

#### 목표

사용자가 한 번에 여러 파일을 선택하거나 드롭해서 업로드할 수 있어야 한다. 각 파일의 설명은 "공통 설명 사용" 또는 "파일별 설명 작성" 중 선택할 수 있어야 한다.

#### 현재 상태

- `FilesPage.tsx`의 업로드 상태는 `UploadState` 단일 파일 기준이다.
- `uploadStore.ts`의 `startPendingUpload`도 파일 1개를 받아 `/api/upload`로 단건 전송한다.
- 백엔드 `/api/upload`와 `UploadService.Create`는 단건 `multipart` 업로드만 지원한다.

#### 구현 범위

- 1차 구현은 백엔드 단건 API를 유지하고, 프론트엔드에서 선택된 여러 파일을 순차 또는 제한 병렬로 `/api/upload`에 전송한다.
- 2차 구현이 필요할 때만 `/api/uploads` 같은 batch API를 추가한다.
- 업로드 모달 상태를 `UploadBatchState`로 확장한다.
  - `files: File[]`
  - `folderId: string`
  - `descriptionMode: "shared" | "per_file"`
  - `sharedDescription: string`
  - `items: Array<{ file, displayName, description, refineEnabled }>`
- 파일별 표시 이름은 기본적으로 확장자를 제외한 파일명으로 채운다.
- PDF는 기존 정책처럼 정제 옵션을 비활성화한다.
- 오디오 파일은 정제 사용 여부를 파일별로 설정할 수 있어야 한다.
- 업로드 진행률은 파일별 pending row로 표시한다.

#### 수용 기준

- 파일 선택창에서 2개 이상 선택하면 하나의 업로드 모달에 모든 파일이 표시된다.
- 공통 설명 모드에서는 모든 파일에 동일한 `description`이 전송된다.
- 파일별 설명 모드에서는 각 파일의 `description`이 독립적으로 전송된다.
- 일부 파일 업로드가 실패해도 나머지 파일 업로드가 계속 진행되고, 실패 항목은 목록에서 실패 상태로 표시된다.
- 업로드 완료 후 SSE 또는 재조회로 실제 작업 row와 pending row가 중복 없이 정리된다.

#### 테스트 포인트

- `uploadStore` 단위 테스트: 여러 파일 업로드 시 pending item 생성/성공/실패 정리.
- 브라우저 수동 테스트: 오디오 2개, PDF 1개 혼합 업로드.
- 백엔드 회귀 테스트: 기존 단건 `/api/upload` 동작 유지.

### 2.2. 드래그 앤 드롭 업로드

#### 목표

파일 목록 영역으로 로컬 파일을 드래그하면 업로드 모달이 열리고, 현재 폴더로 업로드되어야 한다.

#### 현재 상태

- `FilesPage.tsx`에는 앱 내부 항목 이동용 drag/drop 로직이 있다.
- 로컬 파일 드롭 업로드는 분리되어 있지 않다.

#### 구현 범위

- `DataTransfer.files`가 존재하면 외부 파일 드롭으로 판단한다.
- 앱 내부 이동 payload(`application/x-whisper-entries`)와 로컬 파일 드롭을 명확히 구분한다.
- 드롭 가능 영역:
  - 홈: 업로드 대상 폴더는 루트(`folder_id=""`)로 처리한다.
  - 내 파일/폴더: 현재 폴더(`folderId`)로 처리한다.
  - 검색/휴지통: 업로드 드롭을 비활성화하거나 루트 업로드 확인 UI를 띄운다. 1차 구현은 비활성화한다.
- 드래그 오버 시 목록 영역에 drop target 상태를 표시한다.

#### 수용 기준

- 외부 파일을 내 파일 화면에 드롭하면 다중 업로드 모달이 열린다.
- 폴더 이동 drag/drop과 로컬 파일 업로드 drag/drop이 충돌하지 않는다.
- 허용되지 않는 파일 형식은 업로드 시작 전에 사용자에게 표시된다.

### 2.3. 전체 선택 단축키

#### 목표

파일 목록에서 `Ctrl + A`, macOS에서는 `Cmd + A`로 현재 화면의 모든 표시 항목을 선택한다.

#### 현재 상태

- 클릭, Shift 범위 선택, Cmd/Ctrl 개별 선택은 존재한다.
- 전체 선택 단축키는 명시 구현이 없다.

#### 구현 범위

- `FilesPage.tsx`에 keydown listener를 추가한다.
- 입력 필드, textarea, select, contenteditable 내부에서는 브라우저 기본 전체 선택을 유지한다.
- 파일 목록 화면에 포커스가 있거나 일반 문서 영역에서 `Ctrl/Cmd + A`를 누르면 `visibleEntries` 전체를 선택한다.
- 한 번 더 누를 때 토글 해제는 하지 않는다. 전체 선택만 수행한다.

#### 수용 기준

- 검색/필터/페이지네이션 적용 후 보이는 항목만 선택된다.
- 업로드 모달이나 이름 변경 입력 중에는 텍스트 전체 선택이 정상 동작한다.

### 2.4. 페이지네이션 정책 변경

#### 목표

홈은 최근 20개만 표시하고 페이지네이션을 제거한다. 내 파일은 페이지 크기를 20개에서 40개로 늘린다.

#### 현재 상태

- `/api/files`는 `page_size` 기본값 20을 사용한다.
- 프론트엔드는 `fetchFiles`에서 `page_size`를 보내지 않는다.
- 홈도 같은 페이지네이션 컴포넌트를 사용한다.

#### 구현 범위

- 백엔드 `FilesHandlers.Handler`:
  - `view=home`: `page=1`, `page_size=20`으로 고정하고 `total_pages=1`을 반환한다.
  - `view=explore`: 기본 `page_size=40`.
  - `view=search`: 1차 구현은 기존 20 또는 40 중 제품 판단이 필요하다. 권장값은 40.
- 프론트엔드 `FilesPage.tsx`:
  - `viewMode === "home"`이면 페이지네이션 UI를 렌더링하지 않는다.
  - 홈에서는 URL의 `page` query를 무시하거나 제거한다.

#### 수용 기준

- `/files/home?page=3` 접근 시에도 최근 20개만 표시된다.
- `/files/root`와 `/files/folder/:folder_id`는 기본 40개 단위로 페이지가 나뉜다.
- 기존 `page_size` query가 들어와도 상한을 둔다. 권장 최대값은 100.

### 2.5. 반응형 UI 개선

#### 목표

모바일 또는 작은 화면에서도 파일 목록, 업로드 모달, 선택 툴바, 작업 상세 화면이 깨지지 않아야 한다.

#### 현재 상태

- CSS에 일부 반응형 규칙이 있으나, 파일 목록 행/툴바/모달/작업 상세 PDF 대조 보기에서 작은 화면 검증이 필요하다.

#### 구현 범위

- 기준 뷰포트:
  - 모바일: 360x740
  - 작은 태블릿: 768x1024
  - 데스크톱: 1440x900
- 파일 목록:
  - 행 텍스트가 아이콘/버튼과 겹치지 않게 `min-width: 0`, ellipsis, grid 재배치를 적용한다.
  - 선택 툴바는 작은 화면에서 2줄 레이아웃으로 전환한다.
- 업로드 모달:
  - 다중 파일 목록은 모달 내부 스크롤 영역으로 제한한다.
  - 버튼은 화면 폭이 좁으면 하단 고정 또는 2열 이하로 정리한다.
- 작업 상세:
  - PDF 대조 보기는 작은 화면에서 단일 컬럼 또는 탭 전환 방식으로 표시한다.
  - 오디오 컨트롤이 화면 밖으로 넘치지 않는다.

#### 수용 기준

- 위 기준 뷰포트에서 주요 텍스트/버튼/행이 겹치지 않는다.
- 파일명 긴 항목, 태그 많은 항목, 긴 작업 상태 문구가 UI를 깨뜨리지 않는다.
- Playwright 또는 브라우저 스크린샷으로 각 기준 화면을 확인한다.

### 2.6. 유형 필터 확장

#### 목표

목록 필터에서 기존 "폴더/문서" 수준이 아니라 폴더, 파일 전체, 오디오, PDF를 선택할 수 있어야 한다.

#### 현재 상태

- `TypeFilter`는 `'all' | 'folder' | 'document'` 형태다.
- `TYPE_OPTIONS`는 폴더와 문서만 정의되어 있다.
- `matchesJobFilters`는 날짜 필터만 적용하고 유형 필터는 visible entry 구성 단계에서 일부만 반영된다.

#### 구현 범위

- 타입 정의:
  - `TypeFilter = "all" | "folder" | "file" | "audio" | "pdf"`
- 필터 동작:
  - 전체: 폴더 + 모든 작업
  - 폴더: 폴더만
  - 파일: 모든 작업만
  - 오디오: `job.FileType === "audio"` 작업만
  - PDF: `job.FileType === "pdf"` 작업만
- 필터 라벨과 빈 상태 문구를 함께 갱신한다.

#### 수용 기준

- 내 파일 화면에서 오디오 필터를 선택하면 폴더와 PDF 작업이 숨겨진다.
- PDF 필터를 선택하면 PDF 작업만 표시된다.
- 홈/검색/폴더 화면에서 동일한 필터 의미가 유지된다.

### 2.7. 파일 아이콘 개선

#### 목표

`drive-item-icon`이 파일 유형에 맞게 표시되어야 한다. PDF는 일반 파일 모양이 아니라 PDF 또는 문서 아이콘으로 구분되어야 한다.

#### 현재 상태

- 폴더는 `📁`, 작업은 대부분 `🎧`로 표시된다.
- 이동 모달 일부에는 `📄`가 사용된다.

#### 구현 범위

- `fileIconForJob(job)` 유틸을 추가한다.
  - `audio`: 오디오 아이콘
  - `pdf`: PDF/문서 아이콘
  - 기타: 일반 파일 아이콘
- 홈, 내 파일, 검색, 이동 모달 등 job row 렌더링 위치 전체에 동일 유틸을 사용한다.
- 가능하면 텍스트 이모지 대신 기존 디자인 시스템에 맞춘 아이콘 컴포넌트 또는 CSS sprite로 교체한다. 1차 구현은 기존 스타일을 유지하되 유틸로 중복을 제거한다.

#### 수용 기준

- PDF 작업은 목록에서 오디오 아이콘으로 보이지 않는다.
- 같은 작업이 홈/폴더/검색/이동 모달 어디서든 같은 아이콘으로 보인다.

## 3. 기능 수정 요구사항

### 3.1. 전사 정제 과정 2단계 분할

#### 목표

Gemini 정제 과정에서 내용 누락을 줄이기 위해 현재 단일 JSON 정제 단계를 다음 2단계로 나눈다.

1. 전사 결과 다듬기: 타임라인을 유지한 평문 결과 생성
2. 문단화: 다듬어진 타임라인 평문을 현재 UI가 사용하는 JSON 구조로 변환

#### 현재 상태

- `worker.finalizeRefine`은 `LoadTranscriptTimelineText`로 원본 전사를 타임라인 텍스트로 렌더링한다.
- `integrations/gemini.RefineTranscript`가 곧바로 현재 `refined` JSON 구조를 요청한다.
- 결과는 `job_json.kind = refined` 하나로 저장된다.

#### 구현 범위

- Gemini runtime API를 분리한다.
  - `PolishTranscriptTimeline(rawTimeline, description) (string, error)`
  - `StructureTranscriptParagraphs(polishedTimeline, description) (string, error)`
- 저장 kind를 추가한다.
  - `refined_timeline`: 1단계 평문 타임라인 결과
  - `refined`: 2단계 최종 JSON 결과
- worker 상태 표시를 세분화한다.
  - `전사 정제 중: 문장 다듬기`
  - `전사 정제 중: 문단 구성`
- 1단계 성공 후 2단계 실패 시:
  - `refined_timeline`은 보존한다.
  - 재시도 시 가능하면 1단계를 재사용하고 2단계부터 다시 진행한다.
- JSON 검증은 기존 `normalizeRefineResponseJSON`를 유지하되, 2단계 출력에만 적용한다.

#### 프롬프트 요구

- 1단계 프롬프트:
  - 모든 원문 발화를 보존한다.
  - 타임라인 구간 순서를 바꾸지 않는다.
  - 출력은 JSON이 아니라 줄 단위 평문이다.
  - 각 줄은 원본 시작/종료 시간 또는 최소 시작 시간을 포함해야 한다.
- 2단계 프롬프트:
  - 1단계 결과만 입력으로 사용한다.
  - 현재 `paragraph[].paragraph_summary`, `sentence[].start_time`, `sentence[].content` 스키마를 유지한다.
  - `start_time`은 1단계의 시간값에서만 가져온다.

#### 수용 기준

- 정제 성공 시 기존 UI(`JobDetailPage`)가 별도 수정 없이 `refined` JSON을 렌더링한다.
- 2단계 실패 후 재시도하면 1단계 결과가 있으면 이를 재사용한다.
- 정제 결과 문장 수 또는 시간값이 비정상적으로 줄어드는 경우를 감지하는 최소 검증을 둔다. 예: 원본 타임라인 라인 수 대비 최종 sentence 수가 설정 비율 미만이면 실패 처리.

#### 테스트 포인트

- `gemini` normalize/validation 단위 테스트.
- `worker` refine flow 테스트: 전체 성공, 1단계 성공 후 2단계 실패, 재시도.
- 기존 `rerefine` API가 새 kind들을 올바르게 삭제/재생성하는지 확인.

### 3.2. PDF 정제 과정 개선

#### 목표

PDF 추출 품질을 높이고, 배치 처리 중 누락/순서 오류/부분 실패를 더 잘 감지한다.

#### 현재 상태

- PDF는 페이지를 JPEG로 렌더링한 뒤 Gemini에 배치 단위로 전달한다.
- 배치 결과는 `document_chunk_%d` blob으로 저장하고 최종 `document_json`으로 병합한다.
- 이전 배치의 consistency context를 다음 배치에 전달한다.

#### 구현 범위

- 배치 결과 검증 강화:
  - 결과 `pages` 수가 요청한 페이지 수와 일치해야 한다.
  - `page_index`가 요청 범위를 벗어나면 실패 처리한다.
  - 중복 page index는 기존처럼 병합 실패 처리한다.
- 표/수식/이미지 설명 보존 기준 강화:
  - 표는 header row와 data row를 분리하지 않고 현재 schema에 맞춰 저장한다.
  - 수식은 `math_inline`/`math_block`을 우선 사용한다.
  - 이미지/도표는 제목과 설명이 비어 있으면 해당 요소를 만들지 않는다.
- 배치 재시도:
  - 실패 배치 번호, 페이지 범위, 재개 가능 여부를 UI에 명확히 전달한다.
  - 이미 완료된 chunk는 재사용한다.
- 프롬프트를 `docs/prompts` 또는 코드 내 prompt builder 기준으로 관리한다. 가능하면 코드에 긴 prompt를 흩뿌리지 않는다.

#### 수용 기준

- 10페이지 이상 PDF에서 배치별 page index가 최종 `document_json`에 순서대로 병합된다.
- 중간 배치 실패 후 재시도 시 완료된 chunk를 다시 요청하지 않는다.
- 원본 PDF 대조 보기에서 현재 페이지와 추출 텍스트 페이지가 크게 어긋나지 않는다.

## 4. 기능 추가 요구사항

### 4.1. Notion-like 작업 결과 편집

#### 목표

완료된 작업 결과를 브라우저에서 직접 수정하고 저장할 수 있어야 한다.

#### 범위 결정

1차 구현은 "구조화 블록 편집기"가 아니라 결과 Markdown/텍스트 편집부터 시작한다. 이후 블록 단위 편집으로 확장한다.

#### 데이터 모델

- 원본 처리 결과는 보존한다.
  - `transcript_json`
  - `refined`
  - `document_json`
- 사용자 편집본을 별도 kind로 저장한다.
  - `edited_markdown`
  - 필요 시 `edited_json`
- 수정 이력은 1차 구현에서는 최신본만 저장한다. 2차에서 `job_result_revisions` 테이블을 추가한다.

#### API

- `GET /api/jobs/:job_id/edit`
  - 편집 가능한 현재 결과를 Markdown으로 반환한다.
- `PUT /api/jobs/:job_id/edit`
  - `content`, `base_kind`, `base_version`을 받아 저장한다.
- `DELETE /api/jobs/:job_id/edit`
  - 편집본을 삭제하고 원본 결과 표시로 되돌린다.

#### UI

- `JobDetailPage`에 보기/편집 모드를 추가한다.
- 편집 모드에서는 Markdown textarea 또는 경량 editor를 사용한다.
- 저장, 취소, 원본으로 되돌리기 버튼을 제공한다.
- 저장된 편집본이 있으면 다운로드는 기본적으로 편집본을 사용한다. 원본 다운로드도 선택 가능해야 한다.

#### 수용 기준

- 전사 정제본과 PDF 추출 결과 모두 편집할 수 있다.
- 편집본 저장 후 새로고침해도 편집본이 표시된다.
- 원본 결과는 삭제되거나 덮어써지지 않는다.

### 4.2. 작업 결과 기반 후속 작업

#### 목표

완료된 결과를 기반으로 정리본, 통합본 등 새 산출물을 만들 수 있어야 한다.

#### 1차 기능

- 단일 작업 후속 산출물:
  - 요약본
  - 회의록/정리본
  - 액션 아이템
- 여러 작업 통합 산출물:
  - 선택한 작업들의 통합 요약
  - 선택한 작업들의 주제별 통합본

#### 데이터 모델

- 새 테이블 또는 `job_json` kind 확장 중 선택한다.
- 권장 모델:
  - `job_derivatives`
    - `id`
    - `owner_id`
    - `source_job_ids`
    - `kind`
    - `title`
    - `content_markdown`
    - `created_at`
    - `updated_at`
- 1차 구현에서 단일 작업만 지원한다면 `job_json.kind = derivative_summary`로 시작할 수 있다.

#### API

- `POST /api/jobs/:job_id/derivatives`
  - 단일 작업 산출물 생성.
- `GET /api/jobs/:job_id/derivatives`
  - 산출물 목록 조회.
- `GET /api/derivatives/:derivative_id`
  - 산출물 상세 조회.
- `POST /api/derivatives`
  - 여러 작업 기반 산출물 생성.

#### 처리 방식

- 생성 요청은 비동기 작업으로 큐에 넣는다.
- 생성 상태는 SSE 또는 polling으로 갱신한다.
- Gemini 입력은 원본/정제본/편집본 중 사용자가 선택할 수 있게 한다. 기본값은 편집본이 있으면 편집본, 없으면 정제본, 없으면 원본이다.

#### 수용 기준

- 완료된 작업 상세에서 "요약본 만들기"를 실행하면 별도 산출물이 생성된다.
- 파일 목록에서 여러 작업을 선택해 "통합본 만들기"를 실행할 수 있다.
- 산출물은 원본 작업 결과를 덮어쓰지 않는다.

### 4.3. 공유 폴더

#### 목표

사용자가 폴더를 다른 사용자와 공유하고, 공유받은 사용자는 권한에 따라 파일을 조회하거나 편집할 수 있어야 한다.

#### 데이터 모델

- `folder_shares`
  - `id`
  - `folder_id`
  - `owner_id`
  - `shared_with_user_id`
  - `role`: `viewer` | `editor`
  - `created_at`
  - unique `(folder_id, shared_with_user_id)`

#### 권한 정책

- owner:
  - 공유 설정 변경 가능
  - 폴더/작업 생성, 이동, 삭제 가능
- editor:
  - 공유 폴더 안에서 업로드, 이름 변경, 이동 가능
  - 공유 설정 변경 불가
  - 영구 삭제는 1차 구현에서 불가
- viewer:
  - 조회, 다운로드만 가능
  - 편집/삭제/업로드 불가

#### API

- `GET /api/folders/:folder_id/shares`
- `POST /api/folders/:folder_id/shares`
- `PATCH /api/folders/:folder_id/shares/:share_id`
- `DELETE /api/folders/:folder_id/shares/:share_id`
- `GET /api/shared/files`

#### 쿼리/런타임 변경

- 현재 대부분의 조회는 `owner_id == current_user` 기준이다.
- 공유 폴더 조회 시 `owner_id` 필터를 권한 검사 함수로 대체해야 한다.
- 작업 상세, 다운로드, 오디오/PDF 스트리밍도 공유 권한을 확인해야 한다.

#### 수용 기준

- 공유받은 사용자는 자신의 내 파일과 공유 폴더를 구분해서 볼 수 있다.
- viewer는 공유 폴더의 파일을 삭제/이동/수정할 수 없다.
- 공유 해제 즉시 해당 사용자의 접근이 차단된다.

### 4.4. PDF 다운로드

#### 목표

작업 결과를 PDF 파일로 다운로드할 수 있어야 한다.

#### 현재 상태

- 결과 다운로드는 Markdown/text 중심이다.
- PDF 원본 스트리밍은 `/api/jobs/:job_id/pdf`로 지원한다.
- 결과 PDF 생성 엔드포인트는 없다.

#### 구현 범위

- `GET /api/jobs/:job_id/download.pdf` 또는 `GET /download/:job_id/pdf`를 추가한다.
- 입력 콘텐츠 우선순위:
  1. 편집본(`edited_markdown`)
  2. 정제본 Markdown
  3. 원본 전사 Markdown 또는 문서 Markdown
- PDF 렌더러 후보:
  - 서버 내 HTML 템플릿 + headless Chromium
  - Go PDF 라이브러리
  - 외부 도구 사용 시 설치/운영 요구사항 문서화
- 한글 폰트 포함 또는 시스템 폰트 의존성을 명확히 한다.

#### 수용 기준

- 전사 작업과 PDF 추출 작업 모두 결과 PDF 다운로드가 가능하다.
- 한글, 표, 코드블록, 수식이 최소한 읽을 수 있는 형태로 렌더링된다.
- 파일명은 작업 표시명 기반으로 안전하게 생성된다.

## 5. 보안 요구사항

### 5.1. 인증/인가 보안 점검 및 조치

#### 현재 확인된 구조

- 인증은 `ws_auth` JWT 쿠키 기반이다.
- 쿠키는 `HttpOnly`, `SameSite=Strict`, 설정에 따라 `Secure`를 사용한다.
- 비밀번호는 bcrypt로 해시한다.
- JWT secret이 설정되지 않으면 프로세스 시작 시 임시 secret을 생성한다.

#### 점검 항목

- JWT:
  - `JWT_SECRET` 필수화 여부 검토. 운영 환경에서는 미설정 시 서버 시작 실패가 권장된다.
  - issuer, subject, expires 검증이 충분한지 확인한다.
  - 토큰 재발급/만료 정책을 문서화한다.
- 쿠키:
  - 운영 환경에서 `Secure=true` 강제.
  - `SameSite=Strict` 유지.
  - 필요 시 `MaxAge`도 명시한다.
- 로그인/회원가입:
  - 로그인 rate limit 추가.
  - 실패 응답 시간 편차와 계정 존재 여부 노출 여부 점검.
  - 비밀번호 정책 강화. 최소 길이 12자 권장.
  - 이메일 형식 검증 추가.
- CSRF:
  - JSON API는 SameSite Strict에 의존하고 있으나, 레거시 form POST가 남아 있으므로 CSRF 토큰 또는 Origin/Referer 검증이 필요하다.
- 인가:
  - 모든 job/folder/tag/blob/download 엔드포인트가 owner 또는 공유 권한을 확인하는지 테스트로 보장한다.
  - 공유 폴더 도입 시 owner-only 가정을 제거한다.
- 업로드:
  - 확장자뿐 아니라 MIME sniffing 또는 magic bytes 검증을 추가한다.
  - PDF page/image 변환 한도와 temp file 정리 보장.
  - 파일명 응답/다운로드 header injection 방지.
- 보안 헤더:
  - `Content-Security-Policy`
  - `X-Content-Type-Options: nosniff`
  - `Referrer-Policy`
  - `Frame-Options` 또는 CSP `frame-ancestors`

#### 수용 기준

- 인증 없이 `/api/*`, `/download/*`, `/status/*`, `/api/jobs/:id/audio`, `/api/jobs/:id/pdf` 접근 시 401 또는 로그인 redirect가 일관되게 동작한다.
- 다른 사용자의 job/folder id로 접근하면 404 또는 403이 반환된다.
- 운영 설정에서 JWT secret 미설정, insecure cookie 설정이 감지되면 경고 또는 실패한다.

### 5.2. 봇 크롤링 차단 계획

#### 목표

비인가 크롤링과 무의미한 봇 트래픽을 줄이고, 검색엔진이 개인 파일 URL을 수집하지 않게 한다.

#### 구현 범위

- `robots.txt`
  - `/api/`, `/download/`, `/file/`, `/files/`, `/app/` 차단.
- HTML 메타:
  - SPA entry 응답에 `noindex, nofollow` 적용 검토.
- 인증 보호:
  - 현재 인증 middleware로 대부분 보호되지만, 공개 SPA entry 자체도 민감 메타를 포함하지 않게 유지한다.
- Rate limit:
  - 로그인, 회원가입, 업로드, 다운로드에 IP/user 기준 제한.
- User-Agent 기반 차단:
  - 알려진 공격성/비정상 봇만 보조적으로 차단한다.
  - User-Agent만으로 보안을 보장하지 않는다.

#### 수용 기준

- `/robots.txt`가 명시적으로 민감 경로를 disallow한다.
- 비로그인 상태에서 크롤러가 파일 내용 또는 결과 내용을 받을 수 없다.
- 과도한 요청은 429로 제한된다.

## 6. 권장 구현 순서

### Phase 1: 낮은 위험도의 UI 개선

1. 페이지네이션 정책 변경
2. 유형 필터 확장
3. 파일 아이콘 개선
4. 전체 선택 단축키
5. 반응형 UI 1차 정리

### Phase 2: 업로드 경험 개선

1. 다중 파일 선택 상태 모델 도입
2. 단건 `/api/upload`를 재사용한 다중 업로드
3. 드래그 앤 드롭 업로드
4. pending upload 상태/실패 처리 강화

### Phase 3: 처리 품질 개선

1. 전사 정제 2단계 분리
2. `refined_timeline` 저장 kind 추가
3. PDF 배치 검증 강화
4. 실패/재시도 상태 개선

### Phase 4: 결과 활용 기능

1. 결과 편집본 저장
2. PDF 다운로드
3. 단일 작업 후속 산출물
4. 다중 작업 통합 산출물

### Phase 5: 공유/보안

1. 인증/인가 보안 조치
2. robots/rate limit
3. 공유 폴더 데이터 모델
4. 공유 권한 기반 조회/다운로드/수정

## 7. 공통 완료 기준

- `go test ./...` 통과.
- `frontend` 타입 체크 및 lint 통과.
- 주요 화면은 모바일/태블릿/데스크톱 기준에서 시각적으로 확인.
- 기존 레거시 엔드포인트 동작이 깨지지 않는지 최소 회귀 확인.
- 새 API는 owner/권한 검사를 반드시 포함.
- 새 저장 kind 또는 테이블 추가 시 `docs/db-schema.md`와 관련 테스트를 함께 갱신.
