import json
import os
import tempfile
from pathlib import Path

try:
    import portalocker
except Exception:
    portalocker = None

JOBS_PATH = Path('jobs.json')


def load_jobs():
    if not JOBS_PATH.exists():
        return {}
    try:
        with JOBS_PATH.open('r', encoding='utf-8') as f:
            return json.load(f)
    except Exception:
        return {}


def save_jobs(jobs: dict):
    """Atomically write jobs to JSON file. Uses portalocker if available to lock during replace."""
    JOBS_PATH.parent.mkdir(parents=True, exist_ok=True)
    data = json.dumps(jobs, ensure_ascii=False, indent=2)

    fd, tmp_path = tempfile.mkstemp(dir=JOBS_PATH.parent)
    try:
        with os.fdopen(fd, 'w', encoding='utf-8') as tf:
            tf.write(data)
            tf.flush()
            os.fsync(tf.fileno())

        if portalocker is not None:
            # Lock the target file (create if missing) then replace
            with open(JOBS_PATH, 'a+', encoding='utf-8') as lockf:
                try:
                    portalocker.lock(lockf, portalocker.LOCK_EX)
                except Exception:
                    pass
                try:
                    os.replace(tmp_path, JOBS_PATH)
                finally:
                    try:
                        portalocker.unlock(lockf)
                    except Exception:
                        pass
        else:
            try:
                os.replace(tmp_path, JOBS_PATH)
            except Exception:
                # best-effort
                pass
    finally:
        try:
            if os.path.exists(tmp_path):
                os.remove(tmp_path)
        except Exception:
            pass
