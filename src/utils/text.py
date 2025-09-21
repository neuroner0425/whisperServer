from __future__ import annotations

def format_seconds(sec: int | float | None) -> str:
    if sec is None:
        return '-'
    try:
        sec_int = int(sec)
    except Exception:
        return '-'
    h, r = divmod(sec_int, 3600)
    m, s = divmod(r, 60)
    if h:
        return f"{h}:{m:02d}:{s:02d}"
    return f"{m:02d}:{s:02d}"
