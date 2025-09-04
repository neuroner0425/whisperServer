import os
import uuid
from flask import Flask, request, redirect, url_for, render_template, send_file
from werkzeug.utils import secure_filename
import threading
import whisper
import torch

UPLOAD_FOLDER = 'uploads'
RESULT_FOLDER = 'results'
ALLOWED_EXTENSIONS = {'mp3', 'mp4', 'wav', 'm4a'}

app = Flask(__name__)
app.config['UPLOAD_FOLDER'] = UPLOAD_FOLDER
app.config['RESULT_FOLDER'] = RESULT_FOLDER
os.makedirs(UPLOAD_FOLDER, exist_ok=True)
os.makedirs(RESULT_FOLDER, exist_ok=True)

# 작업 상태 관리 (서버 재시작에도 유지)
from job_persist import save_jobs, load_jobs
lock = threading.Lock()
jobs = load_jobs()

# Whisper 모델 로드

# 모델 동적 로딩/해제
device = torch.device('mps' if torch.backends.mps.is_available() else 'cuda' if torch.cuda.is_available() else 'cpu')
model = None

def allowed_file(filename):
    return '.' in filename and filename.rsplit('.', 1)[1].lower() in ALLOWED_EXTENSIONS

def stt_job(job_id, filepath):
    global model
    with lock:
        jobs[job_id]['status'] = '작업 중'
        save_jobs(jobs)
    if model is None:
        import whisper
        model = whisper.load_model("medium", device=device).to(torch.float32)
    result = model.transcribe(filepath, language="Korean", fp16=False)
    # segment별 타임라인 텍스트 생성
    segments = result.get('segments', [])
    timeline_text = ""
    for seg in segments:
        start = int(seg['start'])
        end = int(seg['end'])
        m1, s1 = divmod(start, 60)
        m2, s2 = divmod(end, 60)
        timeline = f"[{m1:02}:{s1:02}~{m2:02}:{s2:02}] "
        timeline_text += timeline + seg['text'].strip() + "\n"
    txt_path = os.path.join(app.config['RESULT_FOLDER'], f'{job_id}.txt')
    with open(txt_path, 'w', encoding='utf-8') as f:
        f.write(timeline_text)
    with lock:
        jobs[job_id]['status'] = '완료'
        jobs[job_id]['result'] = txt_path
        save_jobs(jobs)
    # 업로드된 원본 파일 삭제
    try:
        if os.path.exists(filepath):
            os.remove(filepath)
    except Exception as e:
        print(f"[파일 삭제 오류] {filepath}: {e}")
    # 다음 작업이 없으면 모델 해제
    with lock:
        has_waiting = any(j['status'] in ['작업 대기 중', '작업 중'] for j in jobs.values())
    if not has_waiting:
        del model
        model = None

@app.route('/')
def home():
    return render_template('home.html')

@app.route('/upload', methods=['GET', 'POST'])
def upload_file():
    if request.method == 'POST':
        if 'file' not in request.files:
            return '파일이 없습니다.', 400
        file = request.files['file']
        if file.filename == '':
            return '파일을 선택하세요.', 400
        # 파일명 입력값 받기
        input_name = request.form.get('input_name', '').strip()
        original_filename = file.filename
        ext = os.path.splitext(original_filename)[1]
        if not input_name:
            input_name = os.path.splitext(original_filename)[0]
        # 확장자 중복 방지
        if not input_name.endswith(ext):
            input_name += ext
        safe_filename = secure_filename(original_filename)
        job_id = str(uuid.uuid4())
        save_path = os.path.join(app.config['UPLOAD_FOLDER'], f'{job_id}_{safe_filename}')
        file.save(save_path)
        with lock:
            jobs[job_id] = {'status': '작업 대기 중', 'filename': input_name, 'result': None}
            save_jobs(jobs)
        threading.Thread(target=stt_job, args=(job_id, save_path)).start()
        return redirect(url_for('job_status', job_id=job_id))
    return render_template('upload.html')

@app.route('/jobs')
def job_list():
    with lock:
        job_items = list(jobs.items())[::-1]  # 최신순
    return render_template('jobs.html', job_items=job_items)

@app.route('/job/<job_id>')
def job_status(job_id):
    job = jobs.get(job_id)
    if not job:
        return '작업을 찾을 수 없습니다.', 404
    if job['status'] == '완료':
        # 타임라인별 문단 개행 처리
        with open(job['result'], encoding='utf-8') as f:
            lines = f.readlines()
        html_lines = [f'<div style="margin-bottom:12px;"><span style="color:#2563eb;font-weight:bold;">'+line.split(']')[0]+']</span> '+line.split(']',1)[1].strip()+'</div>' if ']' in line else line for line in lines]
        text = '\n'.join(html_lines)
        return render_template('result.html', job=job, job_id=job_id, text=text)
    else:
        # 진행중/대기중 화면 템플릿 분리
        return render_template('waiting.html', job=job)

@app.route('/download/<job_id>')
def download_txt(job_id):
    job = jobs.get(job_id)
    if not job or job['status'] != '완료':
        return '다운로드할 결과가 없습니다.', 404
    # 입력한 파일명에서 확장자 제거 후 .txt로 저장
    base = os.path.splitext(job['filename'])[0]
    return send_file(job['result'], as_attachment=True, download_name=f'{base}.txt')

if __name__ == '__main__':
    app.run(debug=True)
