import json
import threading

JOBS_FILE = 'jobs.json'
JOBS_LOCK = threading.Lock()

def save_jobs(jobs):
    with JOBS_LOCK:
        with open(JOBS_FILE, 'w', encoding='utf-8') as f:
            json.dump(jobs, f, ensure_ascii=False, indent=2)

def load_jobs():
    try:
        with JOBS_LOCK:
            with open(JOBS_FILE, 'r', encoding='utf-8') as f:
                return json.load(f)
    except Exception:
        return {}
