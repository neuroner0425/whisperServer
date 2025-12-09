from __future__ import annotations
import os
import logging
import time
from typing import Optional
from google import genai
from google.genai import types
from google.genai.types import GenerateContentConfig

from src.config import PROJECT_ROOT, GEMINI_MODEL, BASE_INSTRUCTIONS

_gemini_client: Optional[genai.Client] = None

# 로깅 설정은 기존 app의 logging_config나 root logger를 따르도록 함 (print 대신 logging 사용)

def _read_api_key() -> Optional[str]:
    # 1. 환경변수 확인
    k = os.environ.get("GEMINI_API_KEY") or os.environ.get("API_KEY")
    if k:
        return k.strip()
    
    # 2. 파일 확인 (우선순위: 루트 -> services 상위 등, 기존 로직 유지하되 간결화)
    candidate_files = [
        os.path.join(PROJECT_ROOT, 'gemini_api_key.txt'),
        os.path.join(PROJECT_ROOT, '.gemini_api_key'),
    ]
    for key_file in candidate_files:
        if os.path.exists(key_file):
            try:
                with open(key_file, 'r', encoding='utf-8') as f:
                    content = f.read().strip()
                    if content:
                        logging.info(f"[Gemini] API 키 로드: {key_file}")
                        return content
            except Exception as e:
                logging.warning(f"[Gemini] 키 파일 읽기 실패({key_file}): {e}")
                continue
    return None

def init_once() -> Optional[genai.Client]:
    global _gemini_client
    if _gemini_client is not None:
        return _gemini_client
    
    api_key = _read_api_key()
    if not api_key:
        logging.warning("[Gemini] API 키를 찾을 수 없습니다.")
        return None
    
    try:
        # Vimatrax 방식: Client 생성
        _gemini_client = genai.Client(api_key=api_key)
        # 초기화 성공 로그
        logging.info(f"[Gemini] 클라이언트 초기화 성공 (모델: {GEMINI_MODEL})")
        return _gemini_client
    except Exception as e:
        logging.warning(f"[Gemini 초기화 실패] {e}")
        _gemini_client = None
        return None

def refine_transcript(raw_text: str, description: str | None = None) -> str:
    """
    Vimatrax의 generate_for_batch 스타일을 차용하여 재작성.
    이미지 처리 부분은 제외하고, 텍스트 생성(refining)에 집중.
    """
    client = init_once()
    if client is None:
        raise RuntimeError('Gemini API is not configured')
        
    # 프롬프트 구성 (기존 개선된 프롬프트 유지)
    prompt_text = (
        "[원본 전사문]\n"
        f"\"\"\"\n{raw_text}\n\"\"\"\n\n"
    )
    if description:
        prompt_text += "[설명]\n" + f"\"\"\"\n{description}\n\"\"\"\n\n"
    contents = [prompt_text]
    
    max_retries = 3
    base_delay = 5
    
    for attempt in range(max_retries):
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
            return txt
            
        except Exception as e:
            err_msg = str(e)
            is_rate_limit = "429" in err_msg or "ResourceExhausted" in err_msg or "Quota exceeded" in err_msg
            
            logging.warning(f"[Gemini] 호출 실패 (시도 {attempt+1}/{max_retries}): {e}")
            
            if is_rate_limit:
                if attempt < max_retries - 1:
                    wait_time = base_delay * (2 ** attempt)
                    logging.info(f"[Gemini] Rate limit 대기 중... ({wait_time}초)")
                    time.sleep(wait_time)
                    continue
            
            # Rate limit이 아니거나 재시도 횟수 초과 시
            raise RuntimeError(f'Gemini API call failed: {e}')
            
    return ""
