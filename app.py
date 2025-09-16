import os
import uuid
import threading
import queue
import gc
import logging
import html
from datetime import datetime
from fastapi import FastAPI, Request, UploadFile, Form, HTTPException, Response
import subprocess
from fastapi.responses import RedirectResponse, FileResponse
from fastapi.templating import Jinja2Templates
from werkzeug.utils import secure_filename

# PyTorch가 처음 임포트되기 전에 MPS fallback을 활성화
os.environ.setdefault("PYTORCH_ENABLE_MPS_FALLBACK", "1")

BASE_DIR = os.path.dirname(os.path.abspath(__file__))
UPLOAD_FOLDER = os.path.join(BASE_DIR, 'uploads')
RESULT_FOLDER = os.path.join(BASE_DIR, 'results')
ALLOWED_EXTENSIONS = {'mp3', 'mp4', 'wav', 'm4a'}
MAX_UPLOAD_SIZE_MB = int(os.environ.get('MAX_UPLOAD_SIZE_MB', '512'))  # 기본 512MB
CHUNK_SIZE = 4 * 1024 * 1024  # 4MB
JOB_TIMEOUT_SEC = int(os.environ.get('JOB_TIMEOUT_SEC', '3600'))  # 기본 1시간 타임아웃

app = FastAPI()
from fastapi.staticfiles import StaticFiles
TEMPLATE_DIR = os.path.join(BASE_DIR, 'templates')
templates = Jinja2Templates(directory=TEMPLATE_DIR)

"""로깅 설정"""
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s %(levelname)s %(message)s'
)

# Access log filter: suppress logs for GET /job/{job_id} to reduce noise when viewing results
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
    _access_logger = logging.getLogger('uvicorn.access')
    _access_logger.addFilter(UvicornAccessFilter())
except Exception:
    pass

# static 디렉터리 생성 및 마운트 (템플릿의 url_for('static', ...) 사용 지원)
STATIC_DIR = os.path.join(BASE_DIR, 'static')
os.makedirs(STATIC_DIR, exist_ok=True)
app.mount('/static', StaticFiles(directory=STATIC_DIR), name='static')

# 템플릿에서 Flask 스타일 url_for('static', filename=...) 호출을 지원하기 위한 라우트
from fastapi import HTTPException
from fastapi.responses import FileResponse as _FileResponse

@app.get('/static/{filename}', name='static')
async def _static_filename(filename: str):
    filepath = os.path.join(STATIC_DIR, filename)
    if not os.path.exists(filepath):
        raise HTTPException(status_code=404)
    return _FileResponse(filepath)

os.makedirs(UPLOAD_FOLDER, exist_ok=True)
os.makedirs(RESULT_FOLDER, exist_ok=True)

# 작업 상태 관리 (서버 재시작에도 유지)
from job_persist import save_jobs, load_jobs
lock = threading.Lock()
jobs = load_jobs()

# 작업 큐 및 워커
task_queue = queue.Queue()

# Prometheus metrics (optional, lazy init)
PROM_AVAILABLE = False
JOBS_TOTAL = None
JOBS_IN_PROGRESS = None
JOB_DURATION_SECONDS = None
UPLOAD_BYTES = None
QUEUE_LENGTH = None
_prom_init_done = False
_prom_init_lock = threading.Lock()

def prom_init_once() -> bool:
    """Initialize Prometheus metrics once if library is available."""
    global PROM_AVAILABLE, _prom_init_done
    global JOBS_TOTAL, JOBS_IN_PROGRESS, JOB_DURATION_SECONDS, UPLOAD_BYTES, QUEUE_LENGTH
    if _prom_init_done:
        return PROM_AVAILABLE
    with _prom_init_lock:
        if _prom_init_done:
            return PROM_AVAILABLE
        try:
            from prometheus_client import Counter as _Counter, Gauge as _Gauge, Histogram as _Histogram  # type: ignore
            JOBS_TOTAL = _Counter('whisper_jobs_total', 'Total jobs finished by status', ['status'])
            JOBS_IN_PROGRESS = _Gauge('whisper_jobs_in_progress', 'Jobs currently being processed')
            JOB_DURATION_SECONDS = _Histogram('whisper_job_duration_seconds', 'Duration of jobs in seconds')
            UPLOAD_BYTES = _Counter('whisper_upload_bytes_total', 'Total bytes uploaded')
            QUEUE_LENGTH = _Gauge('whisper_task_queue_size', 'Task queue size')
            PROM_AVAILABLE = True
            try:
                QUEUE_LENGTH.set(0)
            except Exception:
                pass
        except Exception:
            PROM_AVAILABLE = False
        _prom_init_done = True
        return PROM_AVAILABLE

def allowed_file(filename: str) -> bool:
    return '.' in filename and filename.rsplit('.', 1)[1].lower() in ALLOWED_EXTENSIONS


def format_seconds(sec: int) -> str:
    try:
        sec = int(sec)
    except Exception:
        return '-'
    h, r = divmod(sec, 3600)
    m, s = divmod(r, 60)
    if h:
        return f"{h}:{m:02d}:{s:02d}"
    return f"{m:02d}:{s:02d}"

def get_media_duration_ffprobe(path: str):
    """Use ffprobe to get media duration in seconds. Returns int seconds or None."""
    try:
        # ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 <file>
        proc = subprocess.run([
            'ffprobe',
            '-v', 'error',
            '-show_entries', 'format=duration',
            '-of', 'default=noprint_wrappers=1:nokey=1',
            path
        ], capture_output=True, text=True, check=False)
        out = proc.stdout.strip()
        if not out:
            return None
        try:
            f = float(out.splitlines()[0].strip())
            return int(round(f))
        except Exception:
            return None
    except FileNotFoundError:
        # ffprobe not installed
        return None


# Gemini API (optional) — lazy init
_gemini_init_done = False
_gemini_client = None
GEMINI_MODEL = os.environ.get('GEMINI_MODEL', 'gemini-2.5-pro')

def _gemini_init_once():
    global _gemini_init_done, _gemini_client
    if _gemini_init_done:
        return _gemini_client
    # 우선순위: 환경변수 -> 환경변수로 지정한 파일 -> 기본 키 파일들
    api_key = os.environ.get('GEMINI_API_KEY')
    if not api_key:
        key_file_env = os.environ.get('GEMINI_API_KEY_FILE')
        candidates = []
        if key_file_env:
            candidates.append(key_file_env)
        # 기본 위치 후보들
        candidates.append(os.path.join(BASE_DIR, 'gemini_api_key.txt'))
        candidates.append(os.path.join(BASE_DIR, '.gemini_api_key'))
        for p in candidates:
            try:
                if os.path.exists(p):
                    with open(p, 'r', encoding='utf-8') as kf:
                        api_key = kf.read().strip()
                        if api_key:
                            logging.info(f"[Gemini] API 키를 파일에서 로드: {p}")
                            break
            except Exception as e:
                logging.warning(f"[Gemini] 키 파일 읽기 실패({p}): {e}")
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

def _gemini_refine_text(raw_text: str, description: str | None = None) -> str:
    client = _gemini_init_once()
    if client is None:
        raise RuntimeError('Gemini API is not configured')
    prompt = (
        "다음은 녹음된 파일을 전사(STT)한 내용이야. 보면 정확하게 인식되지 못했거나, 관련없는 내용으로 전사된 부분이 있어. 이 부분들을 다듬어서 전사문을 재작성해주라. 최대한 원본을 유지하려고 노력해줘. 재작성된 내용 제외하고는 아무 코멘트도 붙히지 마.\n\n"
        '"""\n' + raw_text + '\n"""\n\n'
    )
    if description:
        prompt += (
            "위 전사문을 설명하면 다음과 같아.\n\n" + '"""\n' + description + '\n"""\n'
        )
    try:
        model = client.GenerativeModel(GEMINI_MODEL)
        resp = model.generate_content(prompt)
        return resp.text.strip() if hasattr(resp, 'text') and resp.text else ''
    except Exception as e:
        logging.exception(f"[Gemini 오류] {e}")
        raise RuntimeError('Gemini API call failed')
    except Exception:
        return None

def worker():
    while True:
        job = task_queue.get()
        try:
            if job is None:
                break
            job_id, filepath = job
            started = datetime.now()
            # 메트릭: 작업 시작(in-progress 증가)
            try:
                if prom_init_once() and 'JOBS_IN_PROGRESS' in globals() and JOBS_IN_PROGRESS is not None:
                    JOBS_IN_PROGRESS.inc()
                if prom_init_once() and 'QUEUE_LENGTH' in globals() and QUEUE_LENGTH is not None:
                    QUEUE_LENGTH.set(max(task_queue.qsize(), 0))
            except Exception:
                pass
            with lock:
                if job_id in jobs:
                    jobs[job_id]['status'] = '작업 중'
                    jobs[job_id]['started_at'] = started.strftime('%Y-%m-%d %H:%M:%S')
                    jobs[job_id]['started_ts'] = started.timestamp()
                    save_jobs(jobs)

            logging.info(f"[작업] whisper.cpp 실행 시작: {job_id}, 파일: {filepath}")

            # whisper.cpp 실행
            output_prefix = filepath  # whisper.cpp는 prefix를 받아 .txt를 붙여 저장
            output_path = f"{filepath}.txt"
            whisper_cli = os.path.join(BASE_DIR, "whisper.cpp", "build", "bin", "whisper-cli")
            model_bin = os.path.join(BASE_DIR, "whisper.cpp", "models", "ggml-large-v3.bin")
            vad_model = os.path.join(BASE_DIR, "whisper.cpp", "models", "ggml-silero-v5.1.2.bin")
            cmd = [
                whisper_cli,
                "-m", model_bin,
                "-l", "ko",
                "--max-context", "0",
                "--no-speech-thold", "0.01",
                "--suppress-nst",
                "--no-prints",
                "--vad",
                "--vad-model", vad_model,
                "--vad-threshold", "0.01",
                "--output-txt", output_prefix
            ]

            import time, re
            # 진행률 초기화: 전처리 중
            with lock:
                if job_id in jobs:
                    jobs[job_id]['phase'] = '전처리 중'
                    jobs[job_id]['progress_percent'] = 0
                    jobs[job_id]['progress_label'] = '전처리 중...'
                    save_jobs(jobs)
            try:
                # 표준 출력 파이프를 통해 줄 단위로 읽기
                proc = subprocess.Popen(
                    cmd,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT,
                    text=True,
                    bufsize=1,
                    universal_newlines=True,
                )
                # 타임아웃 타이머 설정
                timed_out = {'flag': False}
                def _kill_timeout():
                    try:
                        proc.kill()
                    except Exception:
                        pass
                    timed_out['flag'] = True
                timer = threading.Timer(JOB_TIMEOUT_SEC, _kill_timeout)
                timer.start()
                output_path = f"{filepath}.txt"
                total_sec = jobs[job_id].get('media_duration_seconds') or 0
                last_percent = -1
                max_percent = -1
                saw_timeline = False
                # 실시간으로 한 줄씩 읽기 (CR/NL 정규화). 외부 출력은 터미널에 재출력하지 않음.
                for out_line in proc.stdout:
                    if out_line is None:
                        break
                    # 일부 툴은 진행률을 CR(\r)로 덮어씀 -> 줄 단위로 강제 변환해서 파싱만 수행
                    for piece in re.split(r'[\r\n]+', out_line):
                        if piece == '':
                            continue
                        # 타임라인 라인을 파싱하여 진행률 계산
                        line = piece.strip()
                        if '-->' in line:
                            # 색 코드나 앞공백이 있어도 찾도록 search 사용
                            m = re.search(r"\[(\d{2}):(\d{2}):(\d{2}(?:\.\d+)?)\s*-->", line)
                            if m:
                                h, mm, ss = m.groups()
                                try:
                                    start_sec = float(h) * 3600 + float(mm) * 60 + float(ss)
                                except Exception:
                                    start_sec = 0.0
                                percent = int((start_sec / total_sec) * 100) if total_sec else 0
                                # 진행률이 되돌아가지 않도록 보정
                                if percent < max_percent:
                                    percent = max_percent
                                if percent != last_percent:
                                    with lock:
                                        if job_id in jobs:
                                            jobs[job_id]['phase'] = '전사 중'
                                            jobs[job_id]['progress_percent'] = percent
                                            jobs[job_id]['progress_label'] = f"전사 중... {percent}%"
                                            save_jobs(jobs)
                                    last_percent = percent
                                    max_percent = max(max_percent, percent)
                                saw_timeline = True
                        if timed_out['flag']:
                            break
                    if timed_out['flag']:
                        break
                try:
                    return_code = proc.wait(timeout=1)
                except Exception:
                    return_code = proc.poll()
                finally:
                    try:
                        timer.cancel()
                    except Exception:
                        pass
                if return_code != 0:
                    if timed_out['flag']:
                        logging.error(f"[whisper.cpp 타임아웃] 작업 시간 초과로 종료: {job_id}")
                        raise TimeoutError("작업 타임아웃")
                    logging.error(f"[whisper.cpp 오류] 비정상 종료 (code={return_code})")
                    raise RuntimeError("whisper.cpp 실행 실패")
                # 정상 종료 시 100%로 마무리 (타임라인이 하나도 없으면 0% 유지)
                with lock:
                    if job_id in jobs:
                        jobs[job_id]['phase'] = '전사 완료'
                        jobs[job_id]['progress_percent'] = 100 if saw_timeline else jobs[job_id].get('progress_percent', 0)
                        jobs[job_id]['progress_label'] = '전사 완료'
                        save_jobs(jobs)
            except Exception as e:
                logging.exception(f"[실행 오류] {e}")
                with lock:
                    if job_id in jobs:
                        jobs[job_id]['status'] = '실패'
                        if isinstance(e, TimeoutError):
                            jobs[job_id]['status_detail'] = '타임아웃'
                        save_jobs(jobs)
                # 메트릭(실패/타임아웃)
                try:
                    if PROM_AVAILABLE and 'JOBS_TOTAL' in globals():
                        status_label = 'timeout' if isinstance(e, TimeoutError) else 'failure'
                        JOBS_TOTAL.labels(status=status_label).inc()
                except Exception:
                    pass
                continue

            # whisper.cpp가 저장한 txt 파일을 결과로 등록
            timeline_text = ""
            try:
                with open(output_path, 'r', encoding='utf-8') as f:
                    timeline_text = f.read()
            except Exception as e:
                logging.exception(f"[결과 읽기 오류] {e}")
            finally:
                # 중간 산출물(.wav.txt) 정리
                try:
                    if output_path and os.path.exists(output_path):
                        os.remove(output_path)
                        logging.info(f"[파일 삭제] 중간 결과 파일 삭제 완료: {output_path}")
                except Exception as _e:
                    logging.warning(f"[파일 삭제 오류] {output_path}: {_e}")

            txt_path = os.path.join(RESULT_FOLDER, f'{job_id}.txt')
            with open(txt_path, 'w', encoding='utf-8') as f:
                f.write(timeline_text)

            # 전사 완료 직후 자동 정제 시도 (API 키 없으면 건너뜀) — 별도 파일에 저장
            try:
                if _gemini_init_once() is not None:
                    # 업로드 시 사용자가 제공한 설명(선택)을 함께 전달
                    user_desc = None
                    with lock:
                        if job_id in jobs:
                            user_desc = jobs[job_id].get('description')
                    refined = _gemini_refine_text(timeline_text, user_desc)
                    if refined:
                        refined_path = os.path.join(RESULT_FOLDER, f'{job_id}_refined.txt')
                        with open(refined_path, 'w', encoding='utf-8') as rf:
                            rf.write(refined)
                        # 메타데이터에 정제본 경로 저장
                        with lock:
                            if job_id in jobs:
                                jobs[job_id]['result_refined'] = refined_path
                                save_jobs(jobs)
                        logging.info(f"[Gemini] 정제 결과 저장 완료: {refined_path}")
            except Exception as e:
                logging.warning(f"[Gemini 정제 건너뜀] {e}")

            completed = datetime.now()
            completed_ts = completed.timestamp()
            with lock:
                if job_id in jobs:
                    jobs[job_id]['status'] = '완료'
                    jobs[job_id]['result'] = txt_path
                    jobs[job_id]['completed_at'] = completed.strftime('%Y-%m-%d %H:%M:%S')
                    jobs[job_id]['completed_ts'] = completed_ts
                    started_ts = jobs[job_id].get('started_ts')
                    if started_ts:
                        jobs[job_id]['duration'] = format_seconds(int(completed_ts - started_ts))
                    save_jobs(jobs)
            # 메트릭: 성공/소요시간
            try:
                if prom_init_once() and 'JOBS_TOTAL' in globals() and JOBS_TOTAL is not None:
                    JOBS_TOTAL.labels(status='success').inc()
                if prom_init_once() and 'JOB_DURATION_SECONDS' in globals() and JOB_DURATION_SECONDS is not None:
                    JOB_DURATION_SECONDS.observe((completed - started).total_seconds())
            except Exception:
                pass

            # 원본 삭제
            try:
                if filepath and os.path.exists(filepath):
                    os.remove(filepath)
                    logging.info(f"[파일 삭제] 원본 파일 삭제 완료: {filepath}")
            except Exception as e:
                logging.warning(f"[파일 삭제 오류] {filepath}: {e}")

        finally:
            # in-progress 감소 및 큐 길이 갱신
            try:
                if prom_init_once() and 'JOBS_IN_PROGRESS' in globals() and JOBS_IN_PROGRESS is not None:
                    JOBS_IN_PROGRESS.dec()
                if prom_init_once() and 'QUEUE_LENGTH' in globals() and QUEUE_LENGTH is not None:
                    QUEUE_LENGTH.set(max(task_queue.qsize(), 0))
            except Exception:
                pass
            task_queue.task_done()


# 워커 스레드 시작 (싱글 워커로 순차 처리)
worker_thread = threading.Thread(target=worker, daemon=True)
worker_thread.start()


def stt_job(job_id: str, filepath: str):
    task_queue.put((job_id, filepath))
    # 큐 길이 메트릭 갱신
    try:
        if prom_init_once() and 'QUEUE_LENGTH' in globals() and QUEUE_LENGTH is not None:
            QUEUE_LENGTH.set(max(task_queue.qsize(), 0))
    except Exception:
        pass


@app.get('/')
async def home(request: Request):
    return templates.TemplateResponse('home.html', {'request': request})


@app.on_event("startup")
def _startup_requeue_pending():
    """서버 기동 시 미완료 작업을 큐에 복구합니다."""
    try:
        with lock:
            # 상태가 '작업 대기 중' 또는 '작업 중'인 항목 복구
            for job_id, job in list(jobs.items()):
                if job.get('status') in ('작업 대기 중', '작업 중'):
                    # 업로드된 wav 경로 추정: 업로드 폴더에서 job_id prefix를 가진 파일 검색
                    # 기존 코드에서 wav_path 형식: f'{job_id}_{safe_filename}.wav'
                    try:
                        for name in os.listdir(UPLOAD_FOLDER):
                            if name.startswith(job_id) and name.endswith('.wav'):
                                wav_path = os.path.join(UPLOAD_FOLDER, name)
                                if os.path.exists(wav_path):
                                    stt_job(job_id, wav_path)
                                    logging.info(f"[복구] 작업 재큐잉: {job_id} -> {wav_path}")
                                    break
                    except Exception as e:
                        logging.warning(f"[복구 오류] {job_id}: {e}")
    except Exception as e:
        logging.warning(f"[startup 복구 실패] {e}")


@app.get('/healthz')
async def healthz():
    """단순 헬스 체크 엔드포인트"""
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


@app.get('/upload')
async def upload_get(request: Request):
    return templates.TemplateResponse('upload.html', {'request': request})


@app.post('/upload')
async def upload_file(request: Request, file: UploadFile = None, input_name: str = Form(None), description: str | None = Form(None)):
    if not file:
        raise HTTPException(status_code=400, detail='파일이 없습니다.')
    if file.filename == '':
        raise HTTPException(status_code=400, detail='파일을 선택하세요.')
    if not allowed_file(file.filename):
        raise HTTPException(status_code=400, detail=f"허용되지 않는 파일 형식입니다. 허용: {', '.join(sorted(ALLOWED_EXTENSIONS))}")
    # 간단한 MIME 확인(신뢰 불가이지만 1차 필터)
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

    # 파일 저장 (임시 경로) - 스트리밍 저장 및 크기 제한
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
        # 업로드 바이트 메트릭
        try:
            if prom_init_once() and 'UPLOAD_BYTES' in globals() and UPLOAD_BYTES is not None:
                UPLOAD_BYTES.inc(total_bytes)
        except Exception:
            pass
    finally:
        # 메모리 버퍼 비우기 시도(대형 업로드 대비)
        try:
            await file.seek(0)
        except Exception:
            pass

    # ffmpeg로 wav 변환
    try:
        cmd = ['ffmpeg', '-y', '-i', temp_path, wav_path]
        proc = subprocess.run(cmd, capture_output=True, text=True)
        if proc.returncode != 0:
            logging.error(f"[ffmpeg 오류] {proc.stderr}")
            raise RuntimeError("ffmpeg 변환 실패")
    except Exception as e:
        logging.exception(f"[ffmpeg 변환 오류] {e}")
        raise HTTPException(status_code=500, detail='ffmpeg 변환 실패')
    finally:
        # 임시 파일 삭제
        if os.path.exists(temp_path):
            os.remove(temp_path)

    # wav 길이 구하기
    duration = get_media_duration_ffprobe(wav_path)

    with lock:
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
        save_jobs(jobs)

    stt_job(job_id, wav_path)
    return RedirectResponse(url=f"/job/{job_id}", status_code=303)


@app.get('/jobs')
async def job_list(request: Request):
    with lock:
        job_items = list(jobs.items())[::-1]
    return templates.TemplateResponse('jobs.html', {'request': request, 'job_items': job_items})


@app.get('/job/{job_id}')
async def job_status(request: Request, job_id: str):
    job = jobs.get(job_id)
    if not job:
        raise HTTPException(status_code=404, detail='작업을 찾을 수 없습니다.')
    if job['status'] == '완료':
        # 기본은 정제본 우선(있을 경우). 쿼리로 original=true 전달 시 원본 표시.
        show_original = request.query_params.get('original', 'false').lower() in ('1', 'true', 'yes', 'on')
        refined_path = job.get('result_refined')
        # 정제본이 있고, 사용자가 원본 강제 표시를 요청하지 않은 경우 정제본 사용
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
                bar_html = f'<span style="display:inline-block;width:80px;height:8px;background:#eee;border-radius:4px;vertical-align:middle;margin-right:6px;overflow:hidden;"><span style="display:inline-block;height:8px;background:#2563eb;width:{percent}%;border-radius:4px;"></span></span>' if total_sec else ''
                # 내부 f-string을 분리하여 백슬래시 이스케이프가 필요한 상황을 피함
                percent_html = ''
                if total_sec:
                    percent_html = f'<span style="color:#888;font-size:0.95em;">({percent}%)</span>'
                html_lines.append(
                    f'<div style="margin-bottom:4px;">{bar_html}'
                    f'<span style="color:#2563eb;font-weight:bold;">{safe_timeline}</span> '
                    f'{content} {percent_html}</div>'
                )
            else:
                # 정제본은 일반 텍스트 라인으로 출력
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


if __name__ == '__main__':
    import uvicorn
    uvicorn.run("app:app", host="0.0.0.0", port=8000, log_level="info", workers=1)


@app.on_event("shutdown")
def _graceful_shutdown():
    """서버 종료 시 워커를 정상 종료합니다."""
    try:
        task_queue.put_nowait(None)
    except Exception:
        pass
    try:
        worker_thread.join(timeout=5)
    except Exception:
        pass
