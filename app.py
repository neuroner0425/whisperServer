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
    while True:
        job = task_queue.get()
        try:
            if job is None:
                break
            job_id, filepath = job
            started = datetime.now()
            with lock:
                if job_id in jobs:
                    jobs[job_id]['status'] = '작업 중'
                    jobs[job_id]['started_at'] = started.strftime('%Y-%m-%d %H:%M:%S')
                    jobs[job_id]['started_ts'] = started.timestamp()
                    save_jobs(jobs)

            print(f"[작업] whisper.cpp 실행 시작: {job_id}, 파일: {filepath}")

            # whisper.cpp 실행
            output_prefix = filepath  # whisper.cpp는 prefix를 받아 .txt를 붙여 저장
            output_path = f"{filepath}.txt"
            cmd = [
                "./whisper.cpp/build/bin/whisper-cli",
                "-m", "whisper.cpp/models/ggml-large-v3.bin",
                "-l", "ko",
                "--max-context", "0",
                "--no-speech-thold", "0.01",
                "--suppress-nst",
                "--no-prints",
                "--vad",
                "--vad-model", "whisper.cpp/models/ggml-silero-v5.1.2.bin",
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
                output_path = f"{filepath}.txt"
                total_sec = jobs[job_id].get('media_duration_seconds') or 0
                last_percent = -1
                max_percent = -1
                saw_timeline = False
                # 실시간으로 한 줄씩 읽기 (CR/NL 정규화). 외부 출력은 터미널에 재출력하지 않음.
                for out_line in proc.stdout:
                    if out_line is None:
                        break
                    print(f"[whisper.cpp][{job_id[:8]}] {out_line.strip()}", flush=True)
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
                                    print(f"[진행률][{job_id[:8]}] {percent}% ({start_sec:.1f}s/{total_sec}s)", flush=True)
                                    with lock:
                                        if job_id in jobs:
                                            jobs[job_id]['phase'] = '전사 중'
                                            jobs[job_id]['progress_percent'] = percent
                                            jobs[job_id]['progress_label'] = f"전사 중... {percent}%"
                                            save_jobs(jobs)
                                    last_percent = percent
                                    max_percent = max(max_percent, percent)
                                saw_timeline = True
                return_code = proc.wait()
                if return_code != 0:
                    print(f"[whisper.cpp 오류] 비정상 종료 (code={return_code})")
                    raise RuntimeError("whisper.cpp 실행 실패")
                # 정상 종료 시 100%로 마무리 (타임라인이 하나도 없으면 0% 유지)
                with lock:
                    if job_id in jobs:
                        jobs[job_id]['phase'] = '전사 완료'
                        jobs[job_id]['progress_percent'] = 100 if saw_timeline else jobs[job_id].get('progress_percent', 0)
                        jobs[job_id]['progress_label'] = '전사 완료'
                        save_jobs(jobs)
            except Exception as e:
                print(f"[실행 오류] {e}")
                with lock:
                    if job_id in jobs:
                        jobs[job_id]['status'] = '실패'
                        save_jobs(jobs)
                continue

            # whisper.cpp가 저장한 txt 파일을 결과로 등록
            timeline_text = ""
            try:
                with open(output_path, 'r', encoding='utf-8') as f:
                    timeline_text = f.read()
            except Exception as e:
                print(f"[결과 읽기 오류] {e}")

            txt_path = os.path.join(RESULT_FOLDER, f'{job_id}.txt')
            with open(txt_path, 'w', encoding='utf-8') as f:
                f.write(timeline_text)

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

            # 원본 삭제
            try:
                if filepath and os.path.exists(filepath):
                    os.remove(filepath)
                    print(f"[파일 삭제] 원본 파일 삭제 완료: {filepath}")
            except Exception as e:
                print(f"[파일 삭제 오류] {filepath}: {e}")

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
    temp_path = os.path.join(UPLOAD_FOLDER, f'{job_id}_temp{ext}')
    wav_path = os.path.join(UPLOAD_FOLDER, f'{job_id}_{safe_filename}.wav')

    # 파일 저장 (임시 경로)
    with open(temp_path, 'wb') as f:
        content = await file.read()
        f.write(content)

    # ffmpeg로 wav 변환
    try:
        cmd = ['ffmpeg', '-y', '-i', temp_path, wav_path]
        proc = subprocess.run(cmd, capture_output=True, text=True)
        if proc.returncode != 0:
            print(f"[ffmpeg 오류] {proc.stderr}")
            raise RuntimeError("ffmpeg 변환 실패")
    except Exception as e:
        print(f"[ffmpeg 변환 오류] {e}")
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
            'media_duration_seconds': duration if duration else None
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
        with open(job['result'], encoding='utf-8') as f:
            lines = f.readlines()
        html_lines = []
        total_sec = job.get('media_duration_seconds') or 0
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
            if ']' in line:
                left, right = line.split(']', 1)
                timeline = left + ']'
                content = right.strip()
                start_sec = parse_start_sec(left[1:]) if total_sec else 0
                percent = int((start_sec/total_sec)*100) if total_sec else 0
                bar_html = f'<span style="display:inline-block;width:80px;height:8px;background:#eee;border-radius:4px;vertical-align:middle;margin-right:6px;overflow:hidden;"><span style="display:inline-block;height:8px;background:#2563eb;width:{percent}%;border-radius:4px;"></span></span>' if total_sec else ''
                html_lines.append(f'<div style="margin-bottom:4px;">{bar_html}<span style="color:#2563eb;font-weight:bold;">{timeline}</span> {content} <span style="color:#888;font-size:0.95em;">({percent}%)</span></div>')
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
