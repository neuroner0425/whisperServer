<div align="center">

# WhisperServer

**음성 및 영상 파일을 Whisper로 전사하고, Gemini로 가공하세요**

![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/neuroner0425/whisperServer)

</div>

## 주요 기능
- Whisper STT: 업로드 파일을 wav로 변환 후 순차 처리
- 로컬 DB 저장: 작업 메타데이터/업로드 wav/전사 결과/정제 결과를 SQLite(`.run/whisper.db`)에 저장
- 작업 큐/상태 페이지: 대기/진행/정제/완료 상태 실시간 폴링
- Gemini 정제(선택): 전사 결과 문장 정제 및 재정제
- 단건/일괄 다운로드, 일괄 삭제
- Prometheus `/metrics`, 헬스체크 `/healthz`

## Installation
> ⚠️ 프로젝트를 빌드하기 위해서 [Go Programming Language](https://golang.org/)를 먼저 설치하세요.

```bash
git clone https://github.com/neuroner0425/whisperServer.git

cd https://github.com/neuroner0425/whisperServer.git

# Copy Example Configuration and fill out values in app.conf.default
cp app.conf.default app.conf

./install_macos
```

## Usage
```bash
./bin/run-server
```

## Documentation

All relevant document can be found [here](docs/).