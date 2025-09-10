# WhisperServer 배포 체크리스트 및 위험요인 정리

간단히: 이 문서는 현재 `app.py` 기반 서비스(Whisper + FastAPI) 를 배포할 때 발생할 수 있는 주요 리스크, 영향, 그리고 권장 대응책을 우선순위와 함께 정리합니다.

## 요약 체크리스트
- MPS/포크 안정성(권장: 단일 프로세스 / FORCE_CPU 옵션)
- 모델 로드/해제 전략 결정(안정성 vs 성능)
- 업로드 크기 제한 및 파일 검사
- graceful shutdown 및 작업큐 정리
- job_persist 동시성(파일 락) 확인
- 템플릿 XSS 방지(출력 이스케이프)
- 로깅/모니터링/헬스체크 도입
- 의존성 고정(requirements.txt) 및 Dockerfile

## 주요 위험요인 및 권장 대응

1) MPS(GPU) 관련 크래시와 포크 문제
   - 영향: 프로세스 SIGABRT, 워커 종료 → 서비스 중단
   - 권장 대응:
     - 프로덕션에서는 단일 프로세스 실행 권장: `uvicorn app:app --host 0.0.0.0 --port 8000 --workers 1`
     - 테스트/운영 전 `FORCE_CPU=1`로 CPU 모드 점검
     - 필요 시 MPS 대신 CPU로 배포하거나 CUDA 환경으로 이전
     - 코드: `PYTORCH_ENABLE_MPS_FALLBACK=1` 기본 설정 유지

2) 모델 수명주기(로드/캐시/해제)
   - 영향: 모델 로드 지연, 메모리 사용량 급증, 안정성/성능 트레이드오프
   - 권장 대응:
     - 안정성 우선: 현재처럼 작업마다 모델 로드→실행→해제 유지
     - 성능 우선: 워커(프로세스) 당 1회 로드(캐시) 후 주기적 재시작 전략
     - 모델 로드 시간 및 메모리 모니터링 도입

3) 업로드 파일 처리 안전성
   - 영향: 디스크 고갈, DoS, 악성 파일 저장
   - 권장 대응:
     - 업로드 크기 제한(프론트엔드/nginx 또는 엔드포인트에서 체크)
     - MIME/시그니처 검사 및 확장자 검증(현재 확장자 화이트리스트 유지)
     - 저장 위치 권한 최소화 및 UUID 기반 파일명 사용(이미 적용)

4) 비정상 종료 시 작업 유실
   - 영향: 큐에 있던 작업 손실
   - 권장 대응:
     - `job_persist`에 큐 상태(대기중) 보존 및 서버 재시작 시 재입력
     - 장기적으로 Redis/RabbitMQ 같은 외부 큐 도입 권장

5) 동시성·파일 잠금
   - 영향: `jobs.json` 손상 혹은 상태 비일관성
   - 권장 대응: portalocker 등 파일 락 사용 확인 및 동시성 테스트

6) 템플릿 XSS 및 출력 처리
   - 영향: 악성 스크립트 주입 위험
   - 권장 대응: Jinja2 autoescape 사용, 원문 출력 시 이스케이프 유지. `|safe` 제거.

7) 로깅·모니터링 부족
   - 영향: 문제 탐지 지연
   - 권장 대응: Python `logging` 설정, 파일/STDERR 집계, Sentry/Prometheus 연동 권장

8) 타임아웃/리트라이 부재
   - 영향: 무한 대기 워커 발생 가능
   - 권장 대응: 작업 타임아웃(예: 15~30분, 환경에 따라 조정), 실패 시 재시도/사용자 알림

9) 보안(인증/권한)
   - 영향: 무단 업로드/서비스 남용
   - 권장 대응: API 토큰 또는 단순 인증(운영 단계에 맞춰), HTTPS 리버스 프록시 적용

10) 배포/의존성 관리
   - 영향: 환경 차이로 인한 오류
   - 권장 대응: `requirements.txt` 고정, 간단한 `Dockerfile` 작성 및 CI 빌드

## 운영 명령 및 환경 예시

단일 프로세스 uvicorn 실행(권장):

```bash
export PYTORCH_ENABLE_MPS_FALLBACK=1
# 필요시 강제 CPU 모드
export FORCE_CPU=1
uvicorn app:app --host 0.0.0.0 --port 8000 --workers 1
```

개발(내부) 테스트용으로는 `python app.py`로 실행해도 됩니다. 배포 시 `--reload` 사용 금지.

## 우선순위(권장 적용 순서)
- 1순위(긴급): 업로드 크기 제한, graceful shutdown 구현
- 2순위(중): job_persist 동시성 보장, 로깅 설정
- 3순위(중): XSS/출력 이스케이프 검증, 작업 타임아웃
- 4순위(장): 외부 큐 도입, Docker 이미지 및 CI 파이프라인

## 다음 작업(실행 가능한 항목)
1. `requirements.txt` 생성 (현재 가상환경에서 `pip freeze > requirements.txt` 권장)
2. `Dockerfile` 초안 추가
3. `on_shutdown` 훅에 graceful stop/worker join 추가
4. 업로드 최대 크기와 MIME 검사 추가
5. 로깅 모듈으로 print → logging 변경

필요하면 위 항목 중 하나를 직접 코드로 적용해 드리겠습니다. 어느 항목을 먼저 적용할까요?
