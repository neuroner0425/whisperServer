from __future__ import annotations
import os
import logging
from typing import Dict, List, Optional
from google import genai
from google.genai import types
from google.genai.types import GenerateContentConfig

from src.config import PROJECT_ROOT, GEMINI_MODEL, BASE_INSTRUCTIONS

_api_keys: List[str] = []
_current_key_index: int = 0
_clients: Dict[str, genai.Client] = {}

# 로깅 설정은 기존 app의 logging_config나 root logger를 따르도록 함 (print 대신 logging 사용)
def _load_api_keys() -> List[str]:
    """환경 변수 > 파일 순으로 API 키 목록을 로드하고, 개행으로 구분된 여러 키를 순서대로 보관한다."""
    global _api_keys
    if _api_keys:
        return _api_keys

    keys: List[str] = []

    # 1. 환경변수 확인 (단일 키만 지원하므로 가장 먼저 사용)
    env_key = os.environ.get("GEMINI_API_KEY") or os.environ.get("API_KEY")
    if env_key:
        keys.append(env_key.strip())

    # 2. 파일 확인 (개행으로 구분된 여러 키 지원)
    candidate_files = [
        os.path.join(PROJECT_ROOT, 'gemini_api_key.txt'),
        os.path.join(PROJECT_ROOT, '.gemini_api_key'),
    ]
    for key_file in candidate_files:
        if not os.path.exists(key_file):
            continue
        try:
            with open(key_file, 'r', encoding='utf-8') as f:
                lines = [line.strip() for line in f.readlines() if line.strip()]
                if lines:
                    logging.info(f"[Gemini] API 키 로드: {key_file} ({len(lines)}개)")
                    keys.extend(lines)
        except Exception as e:
            logging.warning(f"[Gemini] 키 파일 읽기 실패({key_file}): {e}")

    # 중복 제거(순서 유지)
    seen = set()
    unique_keys = []
    for k in keys:
        if k and k not in seen:
            seen.add(k)
            unique_keys.append(k)

    _api_keys = unique_keys
    if not _api_keys:
        logging.warning("[Gemini] API 키를 찾을 수 없습니다.")
    return _api_keys

def _get_client_for_key(key: str) -> Optional[genai.Client]:
    """키별 클라이언트를 캐싱하여 재사용한다."""
    if key in _clients:
        return _clients[key]
    try:
        _clients[key] = genai.Client(api_key=key)
        logging.info(f"[Gemini] 클라이언트 초기화 성공 (모델: {GEMINI_MODEL})")
        return _clients[key]
    except Exception as e:
        logging.warning(f"[Gemini] 클라이언트 초기화 실패: {e}")
        return None

def _advance_key_index() -> None:
    global _current_key_index
    if not _api_keys:
        return
    _current_key_index = (_current_key_index + 1) % len(_api_keys)

def init_once() -> Optional[genai.Client]:
    """기존 호환용: 사용 가능한 첫 번째 키로 클라이언트를 초기화해 반환한다."""
    keys = _load_api_keys()
    if not keys:
        return None

    tried = 0
    total = len(keys)
    while tried < total:
        key = keys[_current_key_index]
        client = _get_client_for_key(key)
        if client is not None:
            return client
        _advance_key_index()
        tried += 1
    return None

def refine_transcript(raw_text: str, description: str | None = None) -> str:
    """
    Vimatrax의 generate_for_batch 스타일을 차용하여 재작성.
    이미지 처리 부분은 제외하고, 텍스트 생성(refining)에 집중.
    """
    keys = _load_api_keys()
    if not keys:
        raise RuntimeError('Gemini API is not configured')
        
    # 프롬프트 구성 (기존 개선된 프롬프트 유지)
    prompt_text = (
        "[원본 전사문]\n"
        f"\"\"\"\n{raw_text}\n\"\"\"\n\n"
    )
    if description:
        prompt_text += "[설명]\n" + f"\"\"\"\n{description}\n\"\"\"\n\n"
    contents = [prompt_text]
    
    last_error: Exception | None = None
    tried = 0
    total_keys = len(keys)

    # 현재 인덱스에서 시작해 순환하며 시도
    while tried < total_keys:
        key = keys[_current_key_index]
        client = _get_client_for_key(key)
        if client is None:
            last_error = RuntimeError("Failed to initialize Gemini client")
            _advance_key_index()
            tried += 1
            continue

        try:
            resp = client.models.generate_content(
                model=GEMINI_MODEL,
                contents=contents,
                config=GenerateContentConfig(
                    system_instruction=BASE_INSTRUCTIONS,
                    temperature=0.8,
                ),
            )
            txt = resp.text.strip() if (hasattr(resp, 'text') and resp.text) else ""
            _advance_key_index()  # 성공 시 다음 키부터 시작해 라운드 로빈
            return txt
        except Exception as e:
            logging.warning(f"[Gemini] 호출 실패 (키 index={_current_key_index}): {e}")
            last_error = e
            _advance_key_index()  # 실패 시 다음 키로 전환
            tried += 1
            continue

    raise RuntimeError(f'Gemini API call failed after trying {total_keys} key(s): {last_error}')
