from __future__ import annotations
import subprocess
import logging
from typing import Optional


def get_media_duration_ffprobe(path: str) -> Optional[int]:
    """Return media duration in whole seconds using ffprobe, or None if unavailable."""
    try:
        proc = subprocess.run([
            'ffprobe','-v','error','-show_entries','format=duration','-of','default=noprint_wrappers=1:nokey=1', path
        ], capture_output=True, text=True, check=False)
        out = proc.stdout.strip()
        if not out:
            return None
        return int(round(float(out.splitlines()[0].strip())))
    except FileNotFoundError:
        return None
    except Exception as e:
        logging.warning(f"[ffprobe] duration 추출 실패: {e}")
        return None


def convert_to_wav(src: str, dst: str) -> None:
    """Convert media file to wav via ffmpeg."""
    try:
        proc = subprocess.run(['ffmpeg','-y','-i', src, dst], capture_output=True, text=True)
        if proc.returncode != 0:
            raise RuntimeError(proc.stderr[:500])
    except Exception as e:
        raise RuntimeError(f"ffmpeg 변환 실패: {e}") from e
