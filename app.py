import os
import uuid
import threading
import queue
import gc
from datetime import datetime
from fastapi import FastAPI, Request, UploadFile, Form, HTTPException
import subprocess
from fastapi.responses import RedirectResponse, FileResponse
from fastapi.templating import Jinja2Templates
from werkzeug.utils import secure_filename

# PyTorch가 처음 임포트되기 전에 MPS fallback을 활성화
os.environ.setdefault("PYTORCH_ENABLE_MPS_FALLBACK", "1")

UPLOAD_FOLDER = 'uploads'
RESULT_FOLDER = 'results'
ALLOWED_EXTENSIONS = {'mp3', 'mp4', 'wav', 'm4a'}

app = FastAPI()
from fastapi.staticfiles import StaticFiles
templates = Jinja2Templates(directory="templates")

# static 디렉터리 생성 및 마운트 (템플릿의 url_for('static', ...) 사용 지원)
os.makedirs('static', exist_ok=True)
app.mount('/static', StaticFiles(directory='static'), name='static')

# 템플릿에서 Flask 스타일 url_for('static', filename=...) 호출을 지원하기 위한 라우트
from fastapi import HTTPException
from fastapi.responses import FileResponse as _FileResponse

@app.get('/static/{filename}', name='static')
async def _static_filename(filename: str):
    filepath = os.path.join('static', filename)
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
    except Exception:
        return None

def worker():
    """작업 큐를 소비하는 백그라운드 워커.
    - 하나씩 처리. 작업마다 모델을 로드하고 작업 끝나면 명확히 해제.
    """
    while True:
        job = task_queue.get()
        try:
            if job is None:
                break
            job_id, filepath = job
            # 작업 시작 시간 기록
            started = datetime.now()
            with lock:
                if job_id in jobs:
                    jobs[job_id]['status'] = '작업 중'
                    jobs[job_id]['started_at'] = started.strftime('%Y-%m-%d %H:%M:%S')
                    jobs[job_id]['started_ts'] = started.timestamp()
                    save_jobs(jobs)

            timeline_text = ""
            result = {"segments": []}
            model = None
            whisper = None
            device_str = 'cpu'

            try:
                # 지연 임포트로 MPS 초기화를 요청 시점으로 늦춤
                import torch
                import whisper

                # 디바이스 선택
                if os.getenv('FORCE_CPU', '0') == '1':
                    device_str = 'cpu'
                elif hasattr(torch.backends, 'mps') and torch.backends.mps.is_available():
                    device_str = 'mps'
                elif torch.cuda.is_available():
                    device_str = 'cuda'

                print(f"[작업] 선택된 디바이스: {device_str}")

                # 모델 로드
                model = whisper.load_model("medium", device=device_str)
                if device_str != 'cpu':
                    try:
                        model = model.to(torch.float32)
                    except Exception:
                        pass

                # 변환 시도
                try:
                    result = model.transcribe(filepath, language="Korean", fp16=False)
                except Exception as e:
                    print(f"[변환 오류-1차] (device={device_str}) {filepath}: {e}")
                    # MPS 실패 시 CPU 폴백
                    if device_str == 'mps':
                        try:
                            del model
                            gc.collect()
                            model = whisper.load_model("medium", device='cpu')
                            model = model.to(torch.float32)
                            print("[작업] 변환 CPU 폴백 재시도")
                            result = model.transcribe(filepath, language="Korean", fp16=False)
                        except Exception as e2:
                            print(f"[변환 오류-폴백] {filepath}: {e2}")
                            result = {"segments": []}
            except Exception as e:
                print(f"[모델/변환 전체 오류] {e}")
                result = {"segments": []}

            # 결과 정리
            segments = result.get('segments', [])
            for seg in segments:
                start = int(seg.get('start', 0))
                end = int(seg.get('end', 0))
                m1, s1 = divmod(start, 60)
                m2, s2 = divmod(end, 60)
                timeline = f"[{m1:02}:{s1:02}~{m2:02}:{s2:02}] "
                timeline_text += timeline + seg.get('text', '').strip() + "\n"

            # 미디어 전체 길이: ffprobe 우선 사용, 실패 시 세그먼트로 추정
            media_duration_seconds = None
            try:
                media_duration_seconds = get_media_duration_ffprobe(filepath)
            except Exception:
                media_duration_seconds = None
            if media_duration_seconds is None:
                try:
                    if segments:
                        max_end = 0
                        for seg in segments:
                            try:
                                e = float(seg.get('end', 0) or 0)
                            except Exception:
                                e = 0
                            if e > max_end:
                                max_end = e
                        media_duration_seconds = int(round(max_end))
                except Exception:
                    media_duration_seconds = None

            txt_path = os.path.join(RESULT_FOLDER, f'{job_id}.txt')
            try:
                with open(txt_path, 'w', encoding='utf-8') as f:
                    f.write(timeline_text)
                # 완료 시간 및 소요 시간 기록
                completed = datetime.now()
                completed_ts = completed.timestamp()
                duration_seconds = 0
                with lock:
                    if job_id in jobs:
                        jobs[job_id]['status'] = '완료'
                        jobs[job_id]['result'] = txt_path
                        jobs[job_id]['completed_at'] = completed.strftime('%Y-%m-%d %H:%M:%S')
                        jobs[job_id]['completed_ts'] = completed_ts
                        # 미디어 길이 정보 저장
                        if media_duration_seconds is not None:
                            jobs[job_id]['media_duration_seconds'] = media_duration_seconds
                            jobs[job_id]['media_duration'] = format_seconds(media_duration_seconds)
                        else:
                            jobs[job_id]['media_duration_seconds'] = None
                            jobs[job_id]['media_duration'] = None
                        started_ts = jobs[job_id].get('started_ts')
                        if started_ts:
                            duration_seconds = int(completed_ts - started_ts)
                        # 소요 시간을 mm:ss 형식으로 저장
                        jobs[job_id]['duration'] = format_seconds(duration_seconds)
                        save_jobs(jobs)
                # 전사 완료 시점에 업로드 원본 삭제
                try:
                    if filepath and os.path.exists(filepath):
                        os.remove(filepath)
                        print(f"[파일 삭제] 원본 파일 삭제 완료: {filepath}")
                except Exception as e:
                    print(f"[파일 삭제 오류] {filepath}: {e}")
            except Exception as e:
                print(f"[결과 저장 오류] {txt_path}: {e}")
                with lock:
                    if job_id in jobs:
                        jobs[job_id]['status'] = '실패'
                        jobs[job_id]['result'] = None
                        # 실패 시에도 완료 시간과 소요 시간을 기록
                        failed_time = datetime.now()
                        jobs[job_id]['completed_at'] = failed_time.strftime('%Y-%m-%d %H:%M:%S')
                        jobs[job_id]['completed_ts'] = failed_time.timestamp()
                        started_ts = jobs[job_id].get('started_ts')
                        if started_ts:
                            jobs[job_id]['duration'] = str(int(jobs[job_id]['completed_ts'] - started_ts))
                        else:
                            jobs[job_id]['duration'] = '0'
                        save_jobs(jobs)

            # 모델 명확하게 해제
            try:
                try:
                    import torch
                    if 'model' in locals():
                        del model
                    if 'whisper' in locals():
                        del whisper
                    gc.collect()
                    if device_str == 'mps':
                        try:
                            torch.mps.empty_cache()
                        except Exception:
                            pass
                except Exception as e:
                    print(f"[모델 해제 오류] {e}")
            finally:
                pass
        finally:
            task_queue.task_done()


# 워커 스레드 시작 (싱글 워커로 순차 처리)
worker_thread = threading.Thread(target=worker, daemon=True)
worker_thread.start()


def stt_job(job_id: str, filepath: str):
    task_queue.put((job_id, filepath))


@app.get('/')
async def home(request: Request):
    return templates.TemplateResponse('home.html', {'request': request})


@app.get('/upload')
async def upload_get(request: Request):
    return templates.TemplateResponse('upload.html', {'request': request})


@app.post('/upload')
async def upload_file(request: Request, file: UploadFile = None, input_name: str = Form(None)):
    if not file:
        raise HTTPException(status_code=400, detail='파일이 없습니다.')
    if file.filename == '':
        raise HTTPException(status_code=400, detail='파일을 선택하세요.')
    if not allowed_file(file.filename):
        raise HTTPException(status_code=400, detail=f"허용되지 않는 파일 형식입니다. 허용: {', '.join(sorted(ALLOWED_EXTENSIONS))}")

    original_filename = file.filename
    ext = os.path.splitext(original_filename)[1]
    if not input_name:
        input_name = os.path.splitext(original_filename)[0]
    if not input_name.endswith(ext):
        input_name += ext

    safe_filename = secure_filename(original_filename)
    job_id = str(uuid.uuid4())
    save_path = os.path.join(UPLOAD_FOLDER, f'{job_id}_{safe_filename}')

    # 파일 저장
    with open(save_path, 'wb') as f:
        content = await file.read()
        f.write(content)

    with lock:
        uploaded = datetime.now()
        jobs[job_id] = {
            'status': '작업 대기 중',
            'filename': input_name,
            'result': None,
            'uploaded_at': uploaded.strftime('%Y-%m-%d %H:%M:%S'),
            'uploaded_ts': uploaded.timestamp()
        }
        save_jobs(jobs)

    stt_job(job_id, save_path)
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
        with open(job['result'], encoding='utf-8') as f:
            lines = f.readlines()
        html_lines = []
        for line in lines:
            if ']' in line:
                left, right = line.split(']', 1)
                timeline = left + ']'
                content = right.strip()
                html_lines.append(f'<div><span style="color:#2563eb;font-weight:bold;">{timeline}</span> {content}</div>')
            else:
                html_lines.append(line.strip())
        text = '\n'.join(html_lines)
        return templates.TemplateResponse('result.html', {'request': request, 'job': job, 'job_id': job_id, 'text': text})
    else:
        return templates.TemplateResponse('waiting.html', {'request': request, 'job': job})


@app.get('/download/{job_id}')
async def download_txt(job_id: str):
    job = jobs.get(job_id)
    if not job or job['status'] != '완료':
        raise HTTPException(status_code=404, detail='다운로드할 결과가 없습니다.')
    base = os.path.splitext(job['filename'])[0]
    return FileResponse(job['result'], media_type='text/plain', filename=f'{base}.txt')


if __name__ == '__main__':
    import uvicorn
    uvicorn.run("app:app", host="0.0.0.0", port=8000, log_level="info", workers=1)
