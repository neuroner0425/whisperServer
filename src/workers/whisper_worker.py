from __future__ import annotations
import os
import threading
import queue
import logging
import subprocess
from datetime import datetime
from typing import Dict, Optional, Any, List

from ..config import RESULT_FOLDER, JOB_TIMEOUT_SEC, WHISPER_CLI, MODEL_DIR, UPLOAD_FOLDER
from ..utils.text import format_seconds
from ..services import gemini_service
from ..job_persist import load_jobs as _load_jobs, save_jobs as _save_jobs  # reuse existing implementation

class JobStatus:
    PENDING = "작업 대기 중"
    RUNNING = "작업 중"
    REFINING_PENDING = "정제 대기 중"
    REFINING = "정제 중"
    COMPLETED = "완료"
    FAILED = "실패"

# Prometheus metrics (optional, lazy import pattern)
PROM_AVAILABLE = False
JOBS_TOTAL = None
JOBS_IN_PROGRESS = None
JOB_DURATION_SECONDS = None
UPLOAD_BYTES = None
QUEUE_LENGTH = None
_prom_init_done = False

jobs_lock = threading.Lock()
jobs: Dict[str, Dict[str, Any]] = _load_jobs()

task_queue: 'queue.Queue[tuple[str,str]]' = queue.Queue()
worker_threads: List[threading.Thread] = []

def prom_init_once() -> bool:
    global PROM_AVAILABLE, _prom_init_done
    global JOBS_TOTAL, JOBS_IN_PROGRESS, JOB_DURATION_SECONDS, UPLOAD_BYTES, QUEUE_LENGTH
    if _prom_init_done:
        return PROM_AVAILABLE
    with jobs_lock:
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


def _read_file_safe(path: str) -> str:
    try:
        with open(path, 'r', encoding='utf-8') as f:
            return f.read()
    except Exception as e:
        logging.exception(f"[결과 읽기 오류] {e}")
        return ''


def _remove_file(path: Optional[str]):
    if not path:
        return
    try:
        if os.path.exists(path):
            os.remove(path)
    except Exception as e:
        logging.warning(f"[파일 삭제 오류] {path}: {e}")
        
def _set_job(job_id: str, **kwargs):
    with jobs_lock:
        if job_id in jobs:
            for k, v in kwargs.items():
                jobs[job_id][k] = v
            _save_jobs(jobs)
            
def _get_job(job_id: str) -> Optional[Dict[str, Any]]:
    with jobs_lock:
        return jobs.get(job_id)

def _run_whisper(job_id: str, wav_path: str, total_sec: int | None):
    logging.info(f"[whisper] 전사 시작: {job_id}")
    output_path = f"{wav_path}.txt"
    model_bin = os.path.join(MODEL_DIR, 'ggml-large-v3.bin')
    vad_model = os.path.join(MODEL_DIR, 'ggml-silero-v5.1.2.bin')
    cmd = [
        str(WHISPER_CLI), '-m', str(model_bin), '-l', 'ko', '--max-context', '0', '--no-speech-thold', '0.01',
        '--suppress-nst', '--no-prints', '--vad', '--vad-model', str(vad_model), '--vad-threshold', '0.01', '--output-txt', wav_path
    ]
    import re, time
    with jobs_lock:
        if job_id in jobs:
            jobs[job_id]['phase'] = '전처리 중'
            jobs[job_id]['progress_percent'] = 0
            jobs[job_id]['progress_label'] = '전처리 중...'
            _save_jobs(jobs)
    proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, bufsize=1, universal_newlines=True)
    timed_out = {'flag': False}
    def _kill_timeout():
        try:
            proc.kill()
        except Exception:
            pass
        timed_out['flag'] = True
    timer = threading.Timer(JOB_TIMEOUT_SEC, _kill_timeout)
    timer.start()
    last_percent = -1
    max_percent = -1
    saw_timeline = False
    try:
        for out_line in proc.stdout:  # type: ignore
            if out_line is None:
                break
            for piece in re.split(r'[\r\n]+', out_line):
                if not piece:
                    continue
                line = piece.strip()
                if '-->' in line:
                    m = re.search(r"\[(\d{2}):(\d{2}):(\d{2}(?:\.\d+)?)\s*-->", line)
                    if m:
                        h, mm, ss = m.groups()
                        try:
                            start_sec = float(h)*3600 + float(mm)*60 + float(ss)
                        except Exception:
                            start_sec = 0
                        percent = int((start_sec / total_sec) * 100) if total_sec else 0
                        if percent < max_percent:
                            percent = max_percent
                        if percent != last_percent:
                            with jobs_lock:
                                if job_id in jobs:
                                    jobs[job_id]['phase'] = '전사 중'
                                    jobs[job_id]['progress_percent'] = percent
                                    jobs[job_id]['progress_label'] = f"전사 중... {percent}%"
                                    _save_jobs(jobs)
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
                raise TimeoutError('작업 타임아웃')
            raise RuntimeError('whisper.cpp 실행 실패')
        with jobs_lock:
            if job_id in jobs:
                jobs[job_id]['phase'] = '전사 완료'
                if saw_timeline:
                    jobs[job_id]['progress_percent'] = 100
                    jobs[job_id]['progress_label'] = '전사 완료'
                _save_jobs(jobs)
    except Exception:
        raise
    finally:
        pass
    text = _read_file_safe(output_path)
    logging.info(f"[whisper] 전사 완료: {job_id}")
    _remove_file(output_path)
    return text

def _task_transcribe(job_id: str, filepath: str, txt_path: str):
    started = datetime.now()
    started_ts=started.timestamp()
    try:
        if prom_init_once() and JOBS_IN_PROGRESS is not None:
            JOBS_IN_PROGRESS.inc()
        if prom_init_once() and QUEUE_LENGTH is not None:
            QUEUE_LENGTH.set(max(task_queue.qsize(), 0))
    except Exception:
        pass
    _set_job(job_id,
        status=JobStatus.RUNNING,
        started_at=started.strftime('%Y-%m-%d %H:%M:%S'),
        started_ts=started_ts
    )
    total_sec = _get_job(job_id).get('media_duration_seconds')
    try:
        timeline_text = _run_whisper(job_id, filepath, total_sec)
    except Exception as e:
        logging.exception(f"[실행 오류] {e}")
        with jobs_lock:
            if job_id in jobs:
                jobs[job_id]['status'] = JobStatus.FAILED
                if isinstance(e, TimeoutError):
                    jobs[job_id]['status_detail'] = '타임아웃'
                _save_jobs(jobs)
        try:
            if PROM_AVAILABLE and JOBS_TOTAL is not None:
                status_label = 'timeout' if isinstance(e, TimeoutError) else 'failure'
                JOBS_TOTAL.labels(status=status_label).inc()
        except Exception:
            pass
        raise e
    logging.info(f"[작업] 전사 결과 저장: {job_id}")
    with open(txt_path, 'w', encoding='utf-8') as f:
        f.write(timeline_text)
    completed = datetime.now()
    completed_ts = completed.timestamp()
    
    duration = format_seconds(int(completed_ts - started_ts))
    
    try:
        if prom_init_once() and JOBS_TOTAL is not None:
            JOBS_TOTAL.labels(status='success').inc()
        if prom_init_once() and JOB_DURATION_SECONDS is not None:
            JOB_DURATION_SECONDS.observe((completed - started).total_seconds())
    except Exception:
        pass
    
    _set_job(job_id,
            status=JobStatus.REFINING_PENDING,
            completed_at=completed.strftime('%Y-%m-%d %H:%M:%S'),
            completed_ts=completed_ts,
            duration=duration)
    
def _task_refining(job_id, timeline_text: str):
    _set_job(job_id, status=JobStatus.REFINING)
    try:
        if gemini_service.init_once() is not None:
            user_desc = None
            with jobs_lock:
                if job_id in jobs:
                    user_desc = jobs[job_id].get('description')
            refined = gemini_service.refine_transcript(timeline_text, user_desc)
            if refined:
                refined_path = os.path.join(RESULT_FOLDER, f'{job_id}_refined.txt')
                with open(refined_path, 'w', encoding='utf-8') as rf:
                    rf.write(refined)
                with jobs_lock:
                    if job_id in jobs:
                        jobs[job_id]['result_refined'] = refined_path
                        _save_jobs(jobs)
                logging.info(f"[Gemini] 정제 결과 저장: {job_id}")
            else:
                logging.warning(f"[Gemini] 정제 결과 없음")
        else:
            logging.info(f"[Gemini] API 미설정, 정제 건너뜀")
    except Exception as e:
        logging.warning(f"[Gemini 정제 실패/건너뜀] {e}")

def worker_loop(task_queue: 'queue.Queue[tuple[str,str]]'):
    while True:
        job = task_queue.get()
        task_done_called = False
        try:
            if job is None:
                task_done_called = True
                task_queue.task_done()
                break
            job_id, filepath = job
            txt_path = os.path.join(RESULT_FOLDER, f'{job_id}.txt')
            current_job_status = _get_job(job_id).get('status')
            
            if current_job_status not in (JobStatus.PENDING, JobStatus.RUNNING, JobStatus.REFINING_PENDING, JobStatus.REFINING):
                logging.info(f"[작업 건너뜀] 상태: {current_job_status}, 작업ID: {job_id}")
                continue

            # 빈 filepath는 정제만 수행하라는 의미
            if not filepath:
                if current_job_status in (JobStatus.REFINING_PENDING, JobStatus.REFINING):
                    logging.info(f"[정제만 시작] {job_id}")
                    try:
                        current_job_status = JobStatus.REFINING
                        timeline_text = _read_file_safe(txt_path)
                        _task_refining(job_id, timeline_text)
                    except Exception as e:
                        logging.warning(f"[정제 실패] {job_id}: {e}")
                    
                    _set_job(job_id, status=JobStatus.COMPLETED)
                else:
                    logging.warning(f"[정제 요청 무시] 잘못된 상태: {current_job_status}, 작업ID: {job_id}")
                continue

            # 일반적인 전사 작업 처리
            if current_job_status in (JobStatus.PENDING, JobStatus.RUNNING):
                logging.info(f"[작업 시작] {job_id}")
                try:
                    current_job_status = JobStatus.RUNNING
                    _task_transcribe(job_id, filepath, txt_path)
                except Exception as e:
                    logging.warning(f"[작업 실패] {job_id}: {e}")
                    continue
                current_job_status = JobStatus.REFINING_PENDING

            if current_job_status in (JobStatus.REFINING_PENDING, JobStatus.REFINING):
                logging.info(f"[정제 시작] {job_id}")
                try:
                    current_job_status = JobStatus.REFINING
                    timeline_text = _read_file_safe(txt_path)
                    _task_refining(job_id, timeline_text)
                except Exception as e:
                    logging.warning(f"[정제 실패] {job_id}: {e}")
                
            _set_job(job_id, status=JobStatus.COMPLETED, result=txt_path)
            # Remove original wav
            try:
                logging.info(f"[파일 삭제] 원본 삭제: {job_id}")
                _remove_file(filepath)
            except Exception as e:
                logging.warning(f"[파일 삭제 오류] {filepath}: {e}")
        finally:
            try:
                if prom_init_once() and JOBS_IN_PROGRESS is not None:
                    JOBS_IN_PROGRESS.dec()
                if prom_init_once() and QUEUE_LENGTH is not None:
                    QUEUE_LENGTH.set(max(task_queue.qsize(), 0))
            except Exception:
                pass
            if not task_done_called:
                task_queue.task_done()
            
def enqueue_stt(job_id: str, filepath: str):
    task_queue.put((job_id, filepath))
    try:
        if prom_init_once() and QUEUE_LENGTH is not None:
            QUEUE_LENGTH.set(max(task_queue.qsize(), 0))
    except Exception:
        pass

def start_worker():
    logging.info("[worker] 작업자 시작 시도")
    if worker_threads:
        return
    t = threading.Thread(target=worker_loop, args=(task_queue,), daemon=True)
    t.start()
    worker_threads.append(t)
    logging.info("[worker] 작업자 시작 완료")

def requeue_pending():
    try:
        to_enqueue = []
        with jobs_lock:
            for job_id, job in list(jobs.items()):
                if job.get('status') in ('작업 대기 중', '작업 중'):
                    for name in os.listdir(UPLOAD_FOLDER):
                        if name.startswith(job_id) and name.endswith('.wav'):
                            wav_path = os.path.join(UPLOAD_FOLDER, name)
                            if os.path.exists(wav_path):
                                to_enqueue.append((job_id, wav_path))
                                break
        for job_id, wav_path in to_enqueue:
            enqueue_stt(job_id, wav_path)
            logging.info(f"[복구] 작업 재큐잉: {job_id} -> {wav_path}")
    except Exception as e:
        logging.warning(f"[startup 복구 실패] {e}")
        
def shutdown_workers():
    logging.info("[shutdown] 작업자 종료 시도")
    for _ in worker_threads:
        task_queue.put(None)
    for t in worker_threads:
        try:
            t.join(timeout=5)
        except Exception:
            pass
    