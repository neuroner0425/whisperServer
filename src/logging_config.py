"""공통 로깅 설정 모듈.

uvicorn / fastapi 실행 시 uvicorn이 자체적으로 logging 설정을 먼저 구성하면
뒤늦게 호출한 logging.basicConfig 가 무시되거나 (이미 Root logger에 Handler 존재)
원하는 level / formatter 가 적용되지 않는 현상이 발생할 수 있다.

이 모듈은 idempotent 한 setup_logging 함수를 제공하여:
 - 환경변수 LOG_LEVEL (기본 INFO)
 - 환경변수 LOG_FILE  (지정 시 RotatingFileHandler 추가)
 - 환경변수 LOG_JSON=true 일 때 JSON 형식 출력 (간단 구현)
 - 중복 초기화 방지
 - uvicorn.access 과다 로그 선택적 필터링

사용 패턴:
	from .logging_config import setup_logging
	setup_logging()

가급적 애플리케이션 import (특히 workers 모듈 import) 전에 1회 호출.
"""

from __future__ import annotations

import logging
import os
import sys
from logging import Logger
from typing import Optional

import pathlib

_INITIALIZED = False


class UvicornAccessFilter(logging.Filter):
	"""특정 경로 접근 로그를 제거할 수 있는 필터.

	환경변수 LOG_FILTER_ACCESS=1 일 때만 활성화.
	/job/<id> GET/POST 접근이 매우 잦아 noisy 할 경우 필터링.
	"""

	def filter(self, record: logging.LogRecord) -> bool:  # True = keep
		try:
			msg = record.getMessage()
			if 'GET /job/' in msg or 'POST /job/' in msg:
				return False
		except Exception:
			return True
		return True


def _json_formatter(record: logging.LogRecord) -> str:
	import json
	base = {
		"level": record.levelname,
		"logger": record.name,
		"message": record.getMessage(),
		"time": getattr(record, 'asctime', None),
	}
	if record.exc_info:
		# 간단한 traceback 문자열 추가
		import traceback
		base["exc"] = ''.join(traceback.format_exception(*record.exc_info))
	return json.dumps(base, ensure_ascii=False)


class JsonFormatter(logging.Formatter):
	def format(self, record: logging.LogRecord) -> str:  # type: ignore[override]
		# asctime 생성 위해 기본 Formatter 동작 일부 재사용
		record.asctime = self.formatTime(record, self.datefmt)  # type: ignore[attr-defined]
		return _json_formatter(record)


def setup_logging(
	*,
	level: Optional[str | int] = None,
	force: bool = False,
	log_file: Optional[str] = None,
	json: Optional[bool] = None,
	propagate_uvicorn: bool = False,
	unify_uvicorn: Optional[bool] = None,
) -> None:
	"""루트 로거를 구성.

	Parameters
	----------
	level: LOG_LEVEL 환경변수 우선 (default INFO)
	force: True 시 기존 핸들러 제거 후 재구성
	log_file: LOG_FILE 환경변수 우선, 지정 시 파일 핸들러 추가
	json: LOG_JSON 환경변수 우선 (true/1/on) 이면 JSON 포맷터 사용
	propagate_uvicorn: uvicorn.* 로거가 루트로 전파되도록 할지 여부 (기본 False)
	unify_uvicorn: uvicorn 핸들러를 제거하고(중복 출력 방지) 루트 포맷만 사용. None이면 환경변수 LOG_UNIFY_UVICORN(기본: propagate_uvicorn 값) 참고
	"""
	global _INITIALIZED
	if _INITIALIZED and not force:
		return

	env_level = os.getenv("LOG_LEVEL")
	if level is None:
		level = env_level or "INFO"
	if isinstance(level, str):
		level = level.upper()

	if log_file is None:
		log_file = os.getenv("LOG_FILE") or None
	if json is None:
		json_env = os.getenv("LOG_JSON", "false").lower()
		json = json_env in ("1", "true", "yes", "on")

	root = logging.getLogger()

	# 로그 디렉토리 및 per-level 파일 핸들러 추가
	log_dir = os.getenv("LOG_DIR", "log")
	log_dir_path = pathlib.Path(log_dir)
	log_dir_path.mkdir(parents=True, exist_ok=True)

	# 파일별 레벨 필터
	class LevelFilter(logging.Filter):
		def __init__(self, min_level, max_level=None):
			super().__init__()
			self.min_level = min_level
			self.max_level = max_level
		def filter(self, record):
			if record.levelno < self.min_level:
				return False
			if self.max_level is not None and record.levelno > self.max_level:
				return False
			return True

	file_levels = [
		("info.log", logging.INFO, logging.WARNING-1),
		("warning.log", logging.WARNING, logging.ERROR-1),
		("error.log", logging.ERROR, None),
	]
	for fname, min_level, max_level in file_levels:
		fpath = log_dir_path / fname
		fh = logging.FileHandler(fpath, encoding="utf-8")
		fh.setLevel(min_level)
		fh.addFilter(LevelFilter(min_level, max_level))
		if json:
			fh.setFormatter(JsonFormatter())
		else:
			fmt = os.getenv("LOG_FORMAT", "%(asctime)s %(levelname)s [%(name)s] %(message)s")
			fh.setFormatter(logging.Formatter(fmt))
		root.addHandler(fh)

	# unify_uvicorn 결정: 명시 인자 > env > propagate_uvicorn 값
	if unify_uvicorn is None:
		unify_env = os.getenv("LOG_UNIFY_UVICORN")
		if unify_env is not None:
			unify_uvicorn = unify_env.lower() in ("1", "true", "yes", "on")
		else:
			unify_uvicorn = propagate_uvicorn

	if force:
		for h in list(root.handlers):
			root.removeHandler(h)

	if not root.handlers:
		# Stream handler
		if json:
			stream_formatter: logging.Formatter = JsonFormatter()
		else:
			fmt = os.getenv("LOG_FORMAT", "%(asctime)s %(levelname)s [%(name)s] %(message)s")
			stream_formatter = logging.Formatter(fmt)
		sh = logging.StreamHandler(sys.stdout)
		sh.setFormatter(stream_formatter)
		root.addHandler(sh)

		if log_file:
			try:
				from logging.handlers import RotatingFileHandler
				fh = RotatingFileHandler(log_file, maxBytes=10 * 1024 * 1024, backupCount=3, encoding='utf-8')
				fh.setFormatter(stream_formatter if json else logging.Formatter(fmt))  # type: ignore[name-defined]
				root.addHandler(fh)
			except Exception as e:  # pragma: no cover (파일 오류시 stdout 경고)
				root.warning(f"파일 핸들러 생성 실패: {e}")

	root.setLevel(level)

	# uvicorn 로거 조정
	for name in ("uvicorn", "uvicorn.error", "uvicorn.access"):
		lg = logging.getLogger(name)
		lg.propagate = propagate_uvicorn
		# unify 설정 시 uvicorn이 기본으로 추가한 핸들러 제거 → 중복/이중 포맷 방지
		if propagate_uvicorn and unify_uvicorn:
			for h in list(lg.handlers):
				lg.removeHandler(h)
		# access 필터 옵션
		if os.getenv("LOG_FILTER_ACCESS", "0") in ("1", "true", "yes", "on") and name == "uvicorn.access":
			found = any(isinstance(f, UvicornAccessFilter) for f in lg.filters)
			if not found:
				lg.addFilter(UvicornAccessFilter())

	_INITIALIZED = True
	root.debug(
		"Logging initialized (level=%s, json=%s, file=%s, propagate_uvicorn=%s, unify_uvicorn=%s)",
		level, json, log_file, propagate_uvicorn, unify_uvicorn
	)


__all__ = ["setup_logging", "UvicornAccessFilter"]

