import os
import uuid
import queue
import logging
import html
from datetime import datetime
from fastapi import FastAPI, Request, UploadFile, Form, HTTPException, Response
from contextlib import asynccontextmanager
from fastapi.responses import RedirectResponse, FileResponse
from fastapi.templating import Jinja2Templates
from fastapi.staticfiles import StaticFiles
from werkzeug.utils import secure_filename

from .logging_config import setup_logging

# 로깅을 가능한 한 빨리 초기화 (다른 내부 모듈 import 전에)
# uvicorn 기본 스타트업 로그(Started server process, Application startup complete 등) 표시를 원하면
# propagate_uvicorn=True 로 설정한다.
setup_logging(propagate_uvicorn=True)

from .config import (  # noqa: E402
    BASE_DIR, UPLOAD_FOLDER, RESULT_FOLDER, TEMPLATE_DIR, STATIC_DIR,
    ALLOWED_EXTENSIONS, CHUNK_SIZE, MAX_UPLOAD_SIZE_MB
)
from .utils.media import get_media_duration_ffprobe, convert_to_wav  # noqa: E402
from .utils.text import format_seconds  # noqa: E402
from .workers.whisper_worker import jobs, jobs_lock, requeue_pending, shutdown_workers, prom_init_once, UPLOAD_BYTES, JOBS_TOTAL, enqueue_stt, _save_jobs  # noqa: E402

# 환경 설정
os.environ.setdefault("PYTORCH_ENABLE_MPS_FALLBACK", "1")

@asynccontextmanager
async def lifespan(app: FastAPI):
    requeue_pending()
    yield
    shutdown_workers()

app = FastAPI(title="Whisper Server", lifespan=lifespan)
templates = Jinja2Templates(directory=TEMPLATE_DIR)
templates.env.globals['datetime'] = datetime
app.mount('/static', StaticFiles(directory=STATIC_DIR), name='static')

# root logger 사용
logger = logging.getLogger(__name__)

class UvicornAccessFilter(logging.Filter):
    def filter(self, record: logging.LogRecord) -> bool:  # return True to keep, False to drop
        try:
            msg = record.getMessage()
            # uvicorn access log lines typically contain the HTTP method and path like: '"GET /job/<id> '
            if 'GET /job/' in msg or 'POST /job/' in msg:
                return False
        except Exception:
            # on any unexpected issue, don't block the log
            return True
        return True

# Attach filter to uvicorn access logger if present
try:
    logger.debug("Attaching UvicornAccessFilter to uvicorn.access logger (already handled in setup if env enabled)")
    _access_logger = logging.getLogger('uvicorn.access')
    # setup_logging에서 LOG_FILTER_ACCESS 로 제어하므로 여기서는 조건 없이 붙이지 않음
except Exception:
    pass

@app.get('/')
async def home(request: Request):
    return templates.TemplateResponse('home.html', {'request': request})


@app.get('/upload')
async def upload_get(request: Request):
    return templates.TemplateResponse('upload.html', {'request': request})


def _allowed_file(filename: str) -> bool:
    return '.' in filename and filename.rsplit('.', 1)[1].lower() in ALLOWED_EXTENSIONS


@app.post('/upload')
async def upload_file(request: Request, file: UploadFile = None, input_name: str = Form(None), description: str | None = Form(None)):
    if not file:
        raise HTTPException(status_code=400, detail='파일이 없습니다.')
    if file.filename == '':
        raise HTTPException(status_code=400, detail='파일을 선택하세요.')
    if not _allowed_file(file.filename):
        raise HTTPException(status_code=400, detail=f"허용되지 않는 파일 형식입니다. 허용: {', '.join(sorted(ALLOWED_EXTENSIONS))}")
    if file.content_type and not (file.content_type.startswith('audio/') or file.content_type.startswith('video/')):
        raise HTTPException(status_code=400, detail='오디오/비디오 파일만 업로드할 수 있습니다.')

    original_filename = file.filename
    ext = os.path.splitext(original_filename)[1]
    if not input_name:
        input_name = os.path.splitext(original_filename)[0]
    if not input_name.endswith(ext):
        input_name += ext

    safe_filename = secure_filename(original_filename)
    job_id = str(uuid.uuid4())
    temp_path = os.path.join(UPLOAD_FOLDER, f'{job_id}_temp{ext}')
    wav_path = os.path.join(UPLOAD_FOLDER, f'{job_id}_{safe_filename}.wav')

    total_bytes = 0
    try:
        with open(temp_path, 'wb') as f:
            while True:
                chunk = await file.read(CHUNK_SIZE)
                if not chunk:
                    break
                total_bytes += len(chunk)
                if total_bytes > MAX_UPLOAD_SIZE_MB * 1024 * 1024:
                    raise HTTPException(status_code=413, detail=f'업로드 용량 초과({MAX_UPLOAD_SIZE_MB}MB)')
                f.write(chunk)
        try:
            if prom_init_once() and UPLOAD_BYTES is not None:
                UPLOAD_BYTES.inc(total_bytes)
        except Exception:
            pass
    finally:
        try:
            await file.seek(0)
        except Exception:
            pass

    try:
        convert_to_wav(temp_path, wav_path)
    except Exception as e:
        logging.exception(f"[ffmpeg 변환 오류] {e}")
        raise HTTPException(status_code=500, detail='ffmpeg 변환 실패')
    finally:
        if os.path.exists(temp_path):
            os.remove(temp_path)

    duration = get_media_duration_ffprobe(wav_path)

    with jobs_lock:
        uploaded = datetime.now()
        jobs[job_id] = {
            'status': '작업 대기 중',
            'filename': input_name,
            'result': None,
            'uploaded_at': uploaded.strftime('%Y-%m-%d %H:%M:%S'),
            'uploaded_ts': uploaded.timestamp(),
            'duration': duration,
            'media_duration': format_seconds(duration) if duration else '-',
            'media_duration_seconds': duration if duration else None,
            'description': description or None,
        }
        _save_jobs(jobs)

    enqueue_stt(job_id, wav_path)
    return RedirectResponse(url=f"/job/{job_id}", status_code=303)


@app.get('/jobs')
async def job_list(request: Request):
    with jobs_lock:
        job_items = list(jobs.items())[::-1]
    return templates.TemplateResponse('jobs.html', {'request': request, 'job_items': job_items})


@app.get('/job/{job_id}')
async def job_status(request: Request, job_id: str):
    job = jobs.get(job_id)
    if not job:
        raise HTTPException(status_code=404, detail='작업을 찾을 수 없습니다.')
    if job['status'] == '완료':
        show_original = request.query_params.get('original', 'false').lower() in ('1', 'true', 'yes', 'on')
        refined_path = job.get('result_refined')
        use_refined = (bool(refined_path and os.path.exists(refined_path)) and not show_original)
        target_path = refined_path if use_refined else job['result']
        with open(target_path, encoding='utf-8') as f:
            lines = f.readlines()
        html_lines = []
        total_sec = 0 if use_refined else (job.get('media_duration_seconds') or 0)
        def parse_start_sec(timeline):
            import re
            m = re.match(r'\[(\d{2}):(\d{2}):(\d{2}\.\d+)', timeline)
            if m:
                h, m_, s = m.groups()
                return float(h)*3600 + float(m_)*60 + float(s)
            m = re.match(r'\[(\d{2}):(\d{2}):(\d{2})', timeline)
            if m:
                h, m_, s = m.groups()
                return int(h)*3600 + int(m_)*60 + int(s)
            return 0
        for line in lines:
            if not use_refined and ']' in line:
                left, right = line.split(']', 1)
                timeline = left + ']'
                content = html.escape(right.strip())
                safe_timeline = html.escape(timeline)
                start_sec = parse_start_sec(left[1:]) if total_sec else 0
                percent = int((start_sec/total_sec)*100) if total_sec else 0
                bar_html = ''
                if total_sec:
                    bar_html = f'<span style="display:inline-block;width:80px;height:8px;background:#eee;border-radius:4px;vertical-align:middle;margin-right:6px;overflow:hidden;">' \
                               f'<span style="display:inline-block;height:8px;background:#2563eb;width:{percent}%;border-radius:4px;"></span></span>'
                percent_html = f'<span style="color:#888;font-size:0.95em;">({percent}%)</span>' if total_sec else ''
                html_lines.append(
                    f'<div style="margin-bottom:4px;">{bar_html}'
                    f'<span style="color:#2563eb;font-weight:bold;">{safe_timeline}</span> '
                    f'{content} {percent_html}</div>'
                )
            else:
                html_lines.append(html.escape(line.strip()))
        text = '\n'.join(html_lines)
        return templates.TemplateResponse('result.html', {
            'request': request,
            'job': job,
            'job_id': job_id,
            'text': text,
            'variant': 'refined' if use_refined else 'original',
            'has_refined': bool(refined_path and os.path.exists(refined_path)),
            'original_query': 'true' if not use_refined else 'false',
        })
    else:
        return templates.TemplateResponse('waiting.html', {'request': request, 'job': job})


@app.get('/download/{job_id}')
async def download_txt(job_id: str):
    job = jobs.get(job_id)
    if not job or job['status'] != '완료':
        raise HTTPException(status_code=404, detail='다운로드할 결과가 없습니다.')
    base = os.path.splitext(job['filename'])[0]
    return FileResponse(job['result'], media_type='text/plain', filename=f'{base}.txt')


@app.get('/download/{job_id}/refined')
async def download_txt_refined(job_id: str):
    job = jobs.get(job_id)
    if not job or job['status'] != '완료':
        raise HTTPException(status_code=404, detail='다운로드할 결과가 없습니다.')
    refined_path = job.get('result_refined')
    if not refined_path or not os.path.exists(refined_path):
        raise HTTPException(status_code=404, detail='정제본이 없습니다.')
    base = os.path.splitext(job['filename'])[0]
    return FileResponse(refined_path, media_type='text/plain', filename=f'{base}_refined.txt')

@app.get('/healthz')
async def healthz():
    return {"status": "ok"}


@app.get('/metrics')
async def metrics():
    if not prom_init_once():
        raise HTTPException(status_code=404, detail='metrics not available')
    try:
        from prometheus_client import generate_latest as _gen, CONTENT_TYPE_LATEST as _ctype  # type: ignore
        data = _gen()
        return Response(content=data, media_type=_ctype)
    except Exception as e:
        raise HTTPException(status_code=500, detail=f'metrics error: {e}')


if __name__ == '__main__':
    import uvicorn
    uvicorn.run('src.app:app', host='0.0.0.0', port=8000, log_level='info', workers=1)
