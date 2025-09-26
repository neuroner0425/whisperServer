from __future__ import annotations
import os
import logging
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
        "다음은 녹음된 파일을 전사(STT)한 내용이야. 보면 정확하게 인식되지 못했거나, 관련없는 내용으로 전사된 부분이 있어. 이 부분들을 다듬어서 전사문을 재작성해주라. 최대한 원본을 유지하려고 노력해줘. 재작성된 내용 제외하고는 아무 코멘트도 붙히지 마.\n\n" '"""\n' + raw_text + '\n"""\n\n'
    )
    if description:
        prompt += "위 전사문을 설명하면 다음과 같아.\n\n" + '"""\n' + description + '\n"""\n'
    try:
        model = client.GenerativeModel(GEMINI_MODEL)
        resp = model.generate_content(prompt)
        return resp.text.strip() if hasattr(resp, 'text') and resp.text else ''
    except Exception as e:
        logging.exception(f"[Gemini 오류] {e}")
        raise RuntimeError('Gemini API call failed')
