from __future__ import annotations
import os
import logging
import time
from typing import Optional
from src.config import PROJECT_ROOT, GEMINI_MODEL

_gemini_client = None
_gemini_init_done = False


def _read_api_key() -> Optional[str]:
    api_key = os.environ.get('GEMINI_API_KEY')
    if api_key:
        return api_key
    key_file_env = os.environ.get('GEMINI_API_KEY_FILE')
    candidates = []
    if key_file_env:
        candidates.append(key_file_env)
    candidates.append(os.path.join(PROJECT_ROOT, 'gemini_api_key.txt'))
    candidates.append(os.path.join(PROJECT_ROOT, '.gemini_api_key'))
    for p in candidates:
        try:
            if os.path.exists(p):
                with open(p, 'r', encoding='utf-8') as kf:
                    key = kf.read().strip()
                    if key:
                        logging.info(f"[Gemini] API 키 로드: {p}")
                        return key
        except Exception as e:
            logging.warning(f"[Gemini] 키 파일 읽기 실패({p}): {e}")
    return None


def init_once():
    global _gemini_client, _gemini_init_done
    if _gemini_init_done:
        return _gemini_client
    api_key = _read_api_key()
    if not api_key:
        _gemini_init_done = True
        _gemini_client = None
        return None
    try:
        import google.generativeai as genai  # type: ignore
        genai.configure(api_key=api_key)
        _gemini_client = genai
    except Exception as e:
        logging.warning(f"[Gemini 초기화 실패] {e}")
        _gemini_client = None
    _gemini_init_done = True
    return _gemini_client


def refine_transcript(raw_text: str, description: str | None = None) -> str:
    client = init_once()
    if client is None:
        raise RuntimeError('Gemini API is not configured')
    prompt = (
        "아래 제공된 텍스트는 오디오를 자동 전사(STT)한 결과물입니다. "
        "문맥상 어색한 오타, 잘못 인식된 단어, 중복된 표현을 수정하여 자연스러운 문장으로 다듬어 주세요.\n\n"
        "[지침]\n"
        "1. 원본의 의미와 사실 관계를 절대 왜곡하지 마십시오.\n"
        "2. 누락된 내용을 임의로 추측하여 추가하지 마십시오.\n"
        "3. 오직 정제된 텍스트만 출력하고, 인사말이나 설명 등 사족은 일절 붙이지 마십시오.\n\n"
        '"""\n' + raw_text + '\n"""\n\n'
    )
    if description:
        prompt += "위 전사문을 설명하면 다음과 같아.\n\n" + '"""\n' + description + '\n"""\n'
    
    max_retries = 3
    base_delay = 5
    last_exception = None

    for attempt in range(max_retries):
        try:
            model = client.GenerativeModel(GEMINI_MODEL)
            resp = model.generate_content(prompt)
            return resp.text.strip() if hasattr(resp, 'text') and resp.text else ''
        except Exception as e:
            last_exception = e
            err_msg = str(e)
            # Check for common rate limit indicators
            if "429" in err_msg or "ResourceExhausted" in err_msg or "Quota exceeded" in err_msg:
                if attempt < max_retries - 1:
                    wait_time = base_delay * (2 ** attempt)
                    logging.warning(f"[Gemini] Rate limit hit. Retrying in {wait_time}s... (Attempt {attempt+1}/{max_retries})")
                    time.sleep(wait_time)
                    continue
            
            # If not a rate limit error, or retries exhausted, break loop to handle error
            break
            
    # If we are here, it means we failed
    logging.exception(f"[Gemini 오류] {last_exception}")
    raise RuntimeError('Gemini API call failed')
