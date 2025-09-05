# WhisperServer

음성/영상 파일을 업로드하여 Whisper로 STT(음성 인식) 결과를 웹에서 확인하고, 타임라인별로 텍스트를 다운로드할 수 있는 모던 Python Flask 기반 웹 서비스입니다.

---

## 주요 기능
- **Whisper STT**: OpenAI Whisper 모델로 고품질 음성 인식
- **txt 다운로드**: 결과를 txt 파일로 다운로드

---

## 설치 및 실행

### 1. Python 환경 준비
- Python 3.8 ~ 3.11 권장

```bash
python -m venv .venv
source .venv/bin/activate
```

### 2. 종속성 설치

```bash
./install_requirements.sh
```

> PyTorch Nightly(CPU) 및 Flask, Whisper 등 모든 필수 패키지가 자동 설치됩니다.

### 3. 서버 실행

#### 3.1. Gunicorn 설치

아래 명령어로 Gunicorn을 설치하세요.

```bash
pip install gunicorn
```

#### 3.2. Gunicorn으로 서버 실행

WhisperServer의 진입점(app.py)에서 Flask 인스턴스가 `app`으로 선언되어 있으므로 아래와 같이 실행합니다:

```bash
gunicorn -w 2 -b 0.0.0.0:8000 app:app
```

- `-w 2` : 워커 프로세스 수(서버 사양에 따라 조절)
- `-b 0.0.0.0:8000` : 모든 IP에서 8000 포트로 서비스
- `app:app` : app.py의 Flask 인스턴스(app)

## 프로젝트 구조

```
whisperServer/
├── app.py                  # 메인 Flask 서버
├── job_persist.py          # 작업 목록 저장/불러오기
├── install_requirements.sh # 패키지 설치 스크립트
├── requirements.txt        # (참고용) 패키지 목록
├── uploads/                # 업로드 파일 저장 폴더
├── results/                # STT 결과 txt 저장 폴더
├── jobs.json               # 작업 목록 DB (자동 생성)
├── static/
│   └── style.css           # 웹 UI 스타일
└── templates/
    ├── base.html           # 공통 레이아웃
    ├── home.html           # 홈
    ├── upload.html         # 파일 업로드
    ├── jobs.html           # 작업 목록
    ├── result.html         # 결과 보기
    └── waiting.html        # 대기/진행 중
```

---

## 사용법
1. **홈**에서 서비스 소개 및 네비게이션
2. **파일 업로드**에서 음성/영상 파일 선택, (선택) 파일명 입력 후 업로드
3. **작업 목록**에서 상태 확인 및 결과 보기
4. **결과 페이지**에서 타임라인별 텍스트 확인 및 txt 다운로드

---

## 참고/팁
- Whisper 모델은 작업이 있을 때만 메모리에 로드되어 리소스를 절약합니다.
- 서버 재시작 시에도 작업 이력(jobs.json)이 유지됩니다.
- 업로드 파일은 STT 완료 후 자동 삭제되어 저장공간을 절약합니다.
- 대용량 파일/장시간 작업은 서버 사양에 따라 시간이 소요될 수 있습니다.