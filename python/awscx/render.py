"""로그 레벨 색상 / 스파크라인 / pyte 색상 변환 / 바이트 humanize."""
import re


_LEVEL_JSON_RE = re.compile(r'"level"\s*:\s*"?([A-Za-z]+)', re.IGNORECASE)


_LEVEL_WORD_RE = re.compile(
    r'\b(ERROR|WARNING|WARN|INFO|DEBUG|TRACE|FATAL|CRITICAL|SEVERE|NOTICE)\b', re.IGNORECASE)


def log_level_style(line):
    """로그 한 줄에서 레벨을 찾아 Rich 스타일을 반환. 못 찾으면 None."""
    m = _LEVEL_JSON_RE.search(line) or _LEVEL_WORD_RE.search(line)
    if not m:
        return None
    lvl = m.group(1).upper()
    if lvl in ("ERROR", "FATAL", "CRITICAL", "SEVERE"):
        return "bold red"
    if lvl in ("WARNING", "WARN"):
        return "yellow"
    if lvl in ("INFO", "NOTICE"):
        return "green"
    if lvl in ("DEBUG", "TRACE"):
        return "dim"
    return None


def humanize_bytes(n):
    n = float(n)
    for unit in ("B", "KB", "MB", "GB", "TB"):
        if n < 1024:
            return f"{n:.1f}{unit}"
        n /= 1024
    return f"{n:.1f}PB"


_SPARK_BLOCKS = "▁▂▃▄▅▆▇█"


def sparkline(values, lo=None, hi=None):
    """숫자 리스트를 유니코드 블록 스파크라인 문자열로."""
    if not values:
        return ""
    lo = min(values) if lo is None else lo
    hi = max(values) if hi is None else hi
    if hi <= lo:
        return _SPARK_BLOCKS[0] * len(values)
    span = hi - lo
    out = []
    for v in values:
        idx = int((v - lo) / span * (len(_SPARK_BLOCKS) - 1))
        idx = max(0, min(len(_SPARK_BLOCKS) - 1, idx))
        out.append(_SPARK_BLOCKS[idx])
    return "".join(out)


def _pyte_color(c):
    """pyte 색 이름/헥스를 Rich 색으로 변환. default/None 이면 None."""
    if not c or c == "default":
        return None
    if c == "brown":
        return "yellow"
    if c.startswith("bright"):
        rest = c[6:]
        return "bright_" + ("yellow" if rest == "brown" else rest)
    if len(c) == 6 and all(x in "0123456789abcdefABCDEF" for x in c):
        return "#" + c
    return c  # red/green/blue/magenta/cyan/white/black 등은 Rich 가 그대로 인식


def _pyte_style(ch):
    """pyte Char 의 속성을 Rich 스타일 문자열로."""
    parts = []
    fg = _pyte_color(ch.fg)
    bg = _pyte_color(ch.bg)
    if fg:
        parts.append(fg)
    if bg:
        parts.append("on " + bg)
    if ch.bold:
        parts.append("bold")
    if ch.italics:
        parts.append("italic")
    if ch.underscore:
        parts.append("underline")
    if ch.strikethrough:
        parts.append("strike")
    if ch.reverse:
        parts.append("reverse")
    return " ".join(parts)

