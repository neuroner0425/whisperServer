"""레거시 위치.

새로운 모듈화된 FastAPI 애플리케이션 엔트리포인트는 `src/app.py` 입니다.
기존 경로 호환을 위해 이 파일을 유지하며 uvicorn 실행 시
`uvicorn src.app:app` 또는 `python -m src.app` 형태로 실행하세요.
"""

from src.app import app  # noqa: F401  (re-export for backward compatibility)

if __name__ == "__main__":
    import uvicorn
    uvicorn.run("src.app:app", host="0.0.0.0", port=8000, log_level="info", workers=1)
