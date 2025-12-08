from __future__ import annotations
import os
import logging
import time
from typing import Optional
from google import genai
from google.genai import types
from google.genai.types import GenerateContentConfig

from src.config import PROJECT_ROOT, GEMINI_MODEL

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
        "아래 제공된 텍스트는 오디오를 자동 전사(STT)한 결과물입니다. "
        "문맥상 어색한 오타, 잘못 인식된 단어, 중복된 표현을 수정하여 자연스러운 문장으로 다듬어 주세요.\n\n"
        "[지침]\n"
        "1. 원본의 의미와 사실 관계를 절대 왜곡하지 마십시오.\n"
        "2. 누락된 내용을 임의로 추측하여 추가하지 마십시오.\n"
        "3. 오직 정제된 텍스트만 출력하고, 인사말이나 설명 등 사족은 일절 붙이지 마십시오.\n\n"
        '""\n' + raw_text + '\n""\n\n'
    )
    if description:
        prompt_text += "위 전사문을 설명하면 다음과 같아.\n\n" + '""\n' + description + '\n""\n'

    contents = [prompt_text]
    
    # Vimatrax의 에러 처리 및 호출 구조 반영
    # 다만 여기서는 단일 요청이므로 루프(배치)는 없음. 
    # 대신 기존의 Retry 로직을 이 구조 안에 녹여냄.
    
    max_retries = 3
    base_delay = 5
    
    for attempt in range(max_retries):
        try:
            # Vimatrax 스타일 호출: client.models.generate_content(...)
            # config=GenerateContentConfig(...) 사용
            resp = client.models.generate_content(
                model=GEMINI_MODEL,  # config.py에서 설정된 모델 사용 (예: gemini-2.5-flash)
                contents=contents,
                config=GenerateContentConfig(
                    temperature=0.3, # Vimatrax는 0.9였으나 정제 작업엔 낮은 값이 유리하므로 0.3 유지
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
