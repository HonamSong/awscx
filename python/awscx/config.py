"""설정/로깅/상수/placeholder."""
import json
import logging
import os

from ._version import __version__, version_label  # 단일 버전 소스 (awscx/_version.py)


DEFAULT_REGION = "ap-northeast-2"


DEFAULT_COMMAND = "/bin/sh"


CONFIG_DIR = os.path.expanduser("~/.config/awscx")


CONFIG_PATH = os.path.join(CONFIG_DIR, "config.json")


DEFAULT_LOG_PATH = os.path.join(CONFIG_DIR, "awscx.log")


DEFAULT_CONFIG = {
    "region": DEFAULT_REGION,
    "command": DEFAULT_COMMAND,
    "log_enabled": True,
    "log_level": "DEBUG",
    "log_path": DEFAULT_LOG_PATH,
    "monitor_interval_sec": 30,
    "tail_interval_sec": 3,
    "list_rows": 15,
    "terminal_color": True,
    # 기본 OFF: 터미널 네이티브 드래그 선택/복사가 그대로 되게(k9s 유사).
    # 켜면 앱 내 마우스 클릭 가능하지만 텍스트 선택 시 Option+드래그 필요.
    "mouse": False,
}


log = logging.getLogger("awscx")


def load_config():
    """config 파일을 읽는다. 없으면 기본값으로 생성. 누락 키는 기본값으로 채움."""
    cfg = dict(DEFAULT_CONFIG)
    try:
        if not os.path.exists(CONFIG_PATH):
            os.makedirs(CONFIG_DIR, exist_ok=True)
            with open(CONFIG_PATH, "w", encoding="utf-8") as f:
                json.dump(DEFAULT_CONFIG, f, indent=2, ensure_ascii=False)
        else:
            with open(CONFIG_PATH, encoding="utf-8") as f:
                data = json.load(f)
            if isinstance(data, dict):
                cfg.update({k: v for k, v in data.items() if k in DEFAULT_CONFIG})
    except (OSError, ValueError):
        pass
    return cfg


def save_config(cfg):
    try:
        os.makedirs(CONFIG_DIR, exist_ok=True)
        with open(CONFIG_PATH, "w", encoding="utf-8") as f:
            json.dump({k: cfg.get(k, DEFAULT_CONFIG[k]) for k in DEFAULT_CONFIG},
                      f, indent=2, ensure_ascii=False)
        return True
    except OSError:
        return False


def setup_logging(cfg):
    """config 에 따라 파일 로깅 설정(재호출 가능 - 핸들러 초기화)."""
    for h in list(log.handlers):
        log.removeHandler(h)
    if not cfg.get("log_enabled", True):
        log.setLevel(logging.CRITICAL)
        return
    level = getattr(logging, str(cfg.get("log_level", "DEBUG")).upper(), logging.DEBUG)
    log.setLevel(level)
    path = cfg.get("log_path") or DEFAULT_LOG_PATH
    try:
        parent = os.path.dirname(path)
        if parent:
            os.makedirs(parent, exist_ok=True)
        h = logging.FileHandler(path)
    except OSError:
        return
    h.setFormatter(logging.Formatter("%(asctime)s %(levelname)s [%(threadName)s] %(message)s"))
    log.addHandler(h)
    log.debug("===== awscx %s 시작 (log=%s) =====", __version__, path)


OUTPUT_PLACEHOLDER = (
    "상단에서 항목을 선택하세요.\n\n"
    "  Enter  상태\n  s  shell 접속\n  l  로그\n  t  task definition\n\n"
    "서비스에 커서를 두면 우측에 실시간 모니터가\n자동으로 표시됩니다."
)


PROFILE_PLACEHOLDER = (
    "먼저 상단에서 프로파일을 선택하세요.\n\n"
    "프로파일을 고르면(Enter) 접근 대상(ECS/EC2)을\n"
    "선택하는 화면이 나옵니다.\n\n"
    "  /  프로파일 필터\n  q  종료"
)


MODE_PLACEHOLDER = (
    "접근 대상을 선택하세요.\n\n"
    "  1) ECS  (cluster/service exec)\n"
    "  2) EC2  (SSM session)"
)


EC2_PLACEHOLDER = (
    "상단에서 EC2 인스턴스를 선택하세요.\n\n"
    "  Enter  SSM 세션 접속\n"
    "  번호   커서 이동\n\n"
    "SSM=online 인 인스턴스만 접속됩니다.\n"
    "(로그·task definition·모니터는 ECS 전용)"
)

