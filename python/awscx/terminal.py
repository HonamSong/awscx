"""pyte + PTY 임베드 터미널 위젯."""
import fcntl
import os
import signal
import struct
import subprocess
import termios
import threading
import time

import pyte
from rich.text import Text
from textual.binding import Binding
from textual.widget import Widget

from .config import log
from .render import _pyte_style


_KEY_SEQ = {
    "enter": b"\r",
    "backspace": b"\x7f",
    "tab": b"\t",
    "escape": b"\x1b",
    "up": b"\x1b[A",
    "down": b"\x1b[B",
    "right": b"\x1b[C",
    "left": b"\x1b[D",
    "home": b"\x1b[H",
    "end": b"\x1b[F",
    "pageup": b"\x1b[5~",
    "pagedown": b"\x1b[6~",
    "delete": b"\x1b[3~",
    "insert": b"\x1b[2~",
    "space": b" ",
}


class TerminalPane(Widget, can_focus=True):
    """
    PTY 로 자식 프로세스를 띄우고 pyte 로 화면을 렌더링하는 임베드 터미널.

    - 포커스된 상태에서 키 입력을 PTY 로 전달한다.
    - Ctrl+B 로 터미널에서 빠져나와(detach) 목록으로 포커스를 돌린다.
    - 자식이 종료되면 on_exit 콜백을 호출한다.
    """

    DEFAULT_CSS = "TerminalPane { height: 1fr; }"

    # 하단 풋터에 '셸 세션에 들어와 있을 때만' 뜨는 안내(실제 처리는 on_key 가 함).
    # 포커스된 위젯의 바인딩이 Footer 에 표시되는 점을 이용한 표시 전용 항목.
    BINDINGS = [
        Binding("f10", "noop", "나가기", show=True),
        Binding("pageup", "noop", "스크롤▲", show=True),
        Binding("pagedown", "noop", "스크롤▼", show=True),
    ]

    def action_noop(self):
        """표시 전용 바인딩 (실제 키 처리는 on_key). 아무 것도 안 함."""

    def __init__(self, **kwargs):
        super().__init__(**kwargs)
        self._pid = None
        self._fd = None
        self._proc = None
        self._screen = None
        self._stream = None
        self._on_exit = None
        self._done = False
        self._lock = threading.Lock()
        self._dirty = False
        self._read_ended = False
        self._pump_timer = None
        self._started = 0.0
        self._last_output = ""
        self._color = True   # 컬러 렌더링 여부(config 로 제어)
        self._start_retries = 0

    @property
    def active(self):
        return self._pid is not None and not self._done

    # ---- 시작/종료 -----------------------------------------------------
    def start(self, cmd, on_exit=None):
        # 위젯이 아직 레이아웃되지 않아 크기가 0/미소면(=display 전환 직후) winsize 가
        # 잘못 잡혀 프롬프트가 화면 밖으로 숨는다. 크기가 확정될 때까지 재시도.
        if self.size.width <= 1 or self.size.height <= 1:
            if self._start_retries < 20:
                self._start_retries += 1
                log.debug("term.start 지연(size=%s) 재시도 %d",
                          self.size, self._start_retries)
                self.call_after_refresh(lambda: self.start(cmd, on_exit))
                return
            log.warning("term.start: 크기 확정 실패, 최소값으로 진행 size=%s", self.size)
        self._start_retries = 0
        self.stop(silent=True)  # 혹시 이전 세션이 남아있으면 정리
        self._done = False
        self._read_ended = False
        self._dirty = True
        self._on_exit = on_exit
        self._started = time.time()
        self._last_output = ""

        cols = max(self.size.width, 20)
        rows = max(self.size.height, 5)
        # 진단: Textual 화면크기 vs 실제 tty 크기 vs 환경변수 — 불일치 시 프롬프트 잘림 원인.
        try:
            app_size = self.app.size
        except Exception:
            app_size = "?"
        try:
            real_tty = os.get_terminal_size(1)  # stdout 의 실제 tty 크기(ioctl)
            real_tty = f"{real_tty.columns}x{real_tty.lines}"
        except Exception as e:
            real_tty = f"err({e})"
        log.info("term.start cmd=%s widget=%s app=%s real_tty=%s "
                 "env(COLUMNS=%s,LINES=%s) → screen=%dx%d",
                 cmd, self.size, app_size, real_tty,
                 os.environ.get("COLUMNS"), os.environ.get("LINES"), cols, rows)
        with self._lock:
            # HistoryScreen: 위로 넘어간 출력(스크롤백)을 보관 → PageUp 으로 다시 보기.
            # 새 출력이 오면 before_event 가 자동으로 맨 아래로 스냅한다.
            self._screen = pyte.HistoryScreen(cols, rows, history=5000, ratio=0.5)
            self._stream = pyte.ByteStream(self._screen)

        # pty.fork() 대신 openpty + subprocess.Popen 사용:
        # 멀티스레드 프로세스에서 fork 후 파이썬 코드를 돌리면(락 상속) 자식이
        # 멈추거나 죽어 셸이 간헐적으로 튕긴다. Popen 은 C 레벨에서 바로 exec 한다.
        master, slave = os.openpty()
        env = dict(os.environ)
        env["TERM"] = "xterm-256color"
        env["COLUMNS"] = str(cols)
        env["LINES"] = str(rows)
        try:
            # login_tty: 자식에서 slave 를 controlling terminal 로 만들고 0/1/2 로 dup.
            # (session-manager-plugin 은 controlling tty 가 없으면 세션을 바로 닫음)
            self._proc = subprocess.Popen(
                cmd, preexec_fn=lambda: os.login_tty(slave),
                env=env, close_fds=True,
            )
        except Exception as e:
            log.exception("Popen 실패")
            os.close(master)
            os.close(slave)
            with self._lock:
                self._stream.feed(f"실행 실패: {e}\r\n".encode())
                self._dirty = True
            self._read_ended = True
            self._pump_timer = self.set_interval(1 / 10, self._pump)
            return
        os.close(slave)  # 부모는 슬레이브 불필요
        self._pid = self._proc.pid
        self._fd = master
        log.info("popen 성공 pid=%s fd=%s", self._pid, self._fd)
        self._set_winsize(rows, cols)

        thread = threading.Thread(target=self._read_loop, daemon=True)
        thread.start()
        # 렌더/종료감지 펌프 (메인 스레드, 20fps)
        self._pump_timer = self.set_interval(1 / 10, self._pump)
        self.focus()
        # 레이아웃 반영 후 포커스를 한 번 더 확정 (목록으로 포커스가 돌아가는 것 방지)
        self.call_after_refresh(self.focus)
        self.refresh()

    def on_click(self):
        if self.active:
            self.focus()

    def stop(self, silent=False):
        if self._done and self._pid is None:
            return
        log.info("term.stop silent=%s pid=%s", silent, self._pid)
        proc = self._proc
        self._proc = None
        self._pid = self._fd = None
        self._done = True
        if self._pump_timer is not None:
            self._pump_timer.stop()
            self._pump_timer = None
        # 자식(프로세스 그룹)을 죽이면 pty EOF 가 발생 → 리더 스레드가 os.read 에서
        # 빠져나와 fd 를 스스로 닫는다. (메인 스레드에서 os.close(fd) 하면
        # blocked read 와 충돌해 macOS 에서 멈추므로 여기서 close 하지 않는다)
        self._kill_proc(proc)
        if not silent and self._on_exit is not None:
            cb, self._on_exit = self._on_exit, None
            cb()

    @staticmethod
    def _kill_proc(proc):
        if proc is None:
            return
        try:
            os.killpg(proc.pid, signal.SIGKILL)  # login_tty(setsid) 로 proc 이 그룹 리더
        except (ProcessLookupError, PermissionError, OSError):
            try:
                proc.kill()
            except Exception:
                pass

    # ---- PTY 입출력 ----------------------------------------------------
    def _read_loop(self):
        # 읽기/파싱은 스레드에서 (메인 루프 플러딩 방지). 렌더는 _pump 가 담당.
        fd = self._fd
        log.debug("read_loop 시작 fd=%s", fd)
        total = 0
        dbg = bytearray()
        while True:
            try:
                data = os.read(fd, 65536)
            except OSError as e:
                log.debug("read_loop OSError: %s (총 %d bytes)", e, total)
                break
            if not data:
                log.debug("read_loop EOF (총 %d bytes)", total)
                break
            total += len(data)
            if len(dbg) < 4096:
                dbg.extend(data[:4096 - len(dbg)])
            with self._lock:
                if self._stream is not None:
                    try:
                        self._stream.feed(data)
                    except Exception:
                        log.exception("stream.feed 오류")
                self._dirty = True
        self._last_output = bytes(dbg).decode("utf-8", "replace")
        # fd 는 이 스레드에서 닫는다 (메인 스레드 close 로 인한 프리즈 방지)
        try:
            os.close(fd)
        except OSError:
            pass
        self._read_ended = True
        log.debug("read_loop 종료. 출력내용=%r", self._last_output)

    @property
    def last_output(self):
        return self._last_output

    @property
    def session_duration(self):
        return time.time() - self._started if self._started else 0.0

    def _pump(self):
        # 메인 스레드: 갱신된 화면을 그리고, 자식 종료(EOF 또는 프로세스 exit)를 감지해 복귀.
        # 위젯 박스와 pyte 화면 크기가 어긋나면(리사이즈 이벤트 누락 등) 재동기화.
        # → winsize 가 실제 보이는 영역과 항상 일치해 프롬프트가 화면 밖으로 잘리지 않음.
        if self._screen is not None and self.size.height > 1 and self.size.width > 1:
            rows = max(self.size.height, 5)
            cols = max(self.size.width, 20)
            if self._screen.lines != rows or self._screen.columns != cols:
                self._resize_to(rows, cols)
        if self._dirty:
            self._dirty = False
            self.refresh()
        # subprocess.Popen.poll() 은 우리가 소유한 자식만 확인 → 오판/ECHILD 없음
        if self._proc is not None and not self._read_ended:
            rc = self._proc.poll()
            if rc is not None:
                log.info("pump: 자식 프로세스 종료 감지 rc=%s", rc)
                self._read_ended = True
        if self._read_ended and not self._done:
            self._finished()

    def _finished(self):
        # 자식이 스스로 종료(exit/태스크 종료 등)한 경우
        log.info("_finished 호출 -> 복귀")
        self._finish_restore()

    def _finish_restore(self):
        if self._pump_timer is not None:
            self._pump_timer.stop()
            self._pump_timer = None
        proc = self._proc
        self._fd = None       # fd 는 리더 스레드가 닫음
        self._pid = None
        self._proc = None
        if proc is not None and proc.poll() is None:
            self._kill_proc(proc)
        if not self._done:
            self._done = True
            self.refresh()
            if self._on_exit is not None:
                cb, self._on_exit = self._on_exit, None
                cb()

    def _set_winsize(self, rows, cols):
        if self._fd is None:
            return
        try:
            fcntl.ioctl(self._fd, termios.TIOCSWINSZ,
                        struct.pack("HHHH", rows, cols, 0, 0))
        except OSError:
            pass

    # ---- 렌더링 --------------------------------------------------------
    def render(self):
        if self._screen is None:
            return Text("(터미널 비활성)")
        active = self.active
        try:
            # 락은 버퍼/화면 스냅샷만 짧게 (feed 스레드와의 경합/프리즈 방지).
            with self._lock:
                screen = self._screen
                rows, cols = screen.lines, screen.columns
                cy, cx = screen.cursor.y, screen.cursor.x
                # 스크롤백을 보고 있을 땐(HistoryScreen) 커서가 hidden → 커서 블록 숨김
                cursor_hidden = bool(getattr(screen.cursor, "hidden", False))
                if not self._color:
                    display = list(screen.display)   # 모노크롬(가벼움)
                    snap = None
                else:
                    display = None
                    snap = [
                        ([row.get(x) for x in range(cols)] if row is not None else None)
                        for row in (screen.buffer.get(y) for y in range(rows))
                    ]

            show_cursor = active and not cursor_hidden
            text = Text(no_wrap=True, overflow="crop")
            if not self._color:
                # 모노크롬: display 문자열 + 커서만 반전 (escape 시퀀스 최소)
                for y, line in enumerate(display):
                    if show_cursor and y == cy and 0 <= cx <= len(line):
                        text.append(line[:cx])
                        text.append(line[cx] if cx < len(line) else " ", style="reverse")
                        text.append(line[cx + 1:])
                    else:
                        text.append(line)
                    if y != len(display) - 1:
                        text.append("\n")
                return text

            for y in range(rows):
                cells = snap[y]
                run, run_style = [], None
                for x in range(cols):
                    ch = cells[x] if cells is not None else None
                    if ch is None:
                        data, style = " ", ""
                    else:
                        data, style = (ch.data or " "), _pyte_style(ch)
                    if show_cursor and y == cy and x == cx:
                        # 커서 셀에 이미 배경/반전(예: vim 비주얼 선택 하이라이트)이 있으면
                        # reverse 로 덮으면 선택이 가려진다 → 그땐 밑줄로 커서만 표시.
                        if ch is not None and (ch.reverse or
                                               (ch.bg and ch.bg != "default")):
                            style = (style + " underline").strip()
                        else:
                            style = (style + " reverse").strip()
                    if style != run_style:
                        if run:
                            text.append("".join(run), style=run_style or "")
                        run, run_style = [data], style
                    else:
                        run.append(data)
                if run:
                    text.append("".join(run), style=run_style or "")
                if y != rows - 1:
                    text.append("\n")
            return text
        except Exception:
            log.exception("terminal render 오류")
            return Text("(render 오류)")

    # ---- 이벤트 --------------------------------------------------------
    def _resize_to(self, rows, cols):
        """pyte 화면과 자식 winsize 를 rows×cols 로 맞춘다."""
        with self._lock:
            if self._screen is None:
                return
            if self._screen.lines == rows and self._screen.columns == cols:
                return
            try:
                self._screen.resize(rows, cols)
            except Exception:
                log.exception("screen.resize 오류")
        log.info("term.resize -> %dx%d", cols, rows)
        self._set_winsize(rows, cols)
        self.refresh()

    def on_resize(self, event):
        if not self.active or self._screen is None:
            return
        self._resize_to(max(self.size.height, 5), max(self.size.width, 20))

    # ---- 스크롤백 ------------------------------------------------------
    def _scroll(self, up, lines=None):
        """스크롤백을 위/아래로 이동. 실제로 이동했으면 True.

        lines 를 주면 그 줄 수만큼(휠용), 없으면 한 페이지(ratio 기본값, 키용).
        HistoryScreen 이 아니거나 더 이동할 내역이 없으면 False → 호출측에서
        해당 키를 원격(PTY)으로 전달하게 한다.
        """
        scr = self._screen
        if scr is None or not hasattr(scr, "history"):
            return False
        with self._lock:
            before = scr.history.position
            saved_ratio = scr.history.ratio
            try:
                if lines is not None:
                    # 휠 한 칸 = 몇 줄만: ratio 를 잠깐 작게 바꿔서 이동
                    r = max(1, int(lines)) / float(max(scr.lines, 1))
                    scr.history = scr.history._replace(ratio=r)
                scr.prev_page() if up else scr.next_page()
            except Exception:
                log.exception("scroll 오류")
                return False
            finally:
                if lines is not None:
                    scr.history = scr.history._replace(ratio=saved_ratio)
            moved = scr.history.position != before
            if moved:
                self._dirty = True
        if moved:
            self.refresh()
        return moved

    def on_mouse_scroll_up(self, event):
        # config mouse=True 일 때만 도착(기본 False 면 발생 안 함) — 있으면 스크롤백.
        if self.active and self._scroll(up=True, lines=3):
            event.stop()

    def on_mouse_scroll_down(self, event):
        if self.active and self._scroll(up=False, lines=3):
            event.stop()

    def on_key(self, event):
        if not self.active or self._fd is None:
            return
        key = event.key

        # F10 / Ctrl+B: 터미널에서 빠져나오기 (detach; 세션 종료)
        # (Ctrl+B 는 tmux prefix 와 충돌할 수 있어 F10 을 기본으로 권장)
        if key in ("f10", "ctrl+b"):
            event.stop()
            event.prevent_default()
            self.stop()
            return

        # 스크롤백(위로 넘어간 출력 다시 보기)
        #  - Shift/Ctrl+PageUp/Down : 무조건 로컬 스크롤(원격에 전달 안 함)
        #  - PageUp/Down            : 스크롤백이 있으면 로컬, 없으면 원격(vim/less)로 전달
        if key in ("shift+pageup", "ctrl+pageup"):
            event.stop(); event.prevent_default()
            self._scroll(up=True)
            return
        if key in ("shift+pagedown", "ctrl+pagedown"):
            event.stop(); event.prevent_default()
            self._scroll(up=False)
            return
        if key == "pageup":
            # 이미 스크롤백을 보는 중이면(position<size) 맨 위에 도달해도 소비만 한다.
            # (원격으로 전달하면 그 응답 feed 로 화면이 맨 아래로 스냅되어 버림)
            scr = self._screen
            scrolled = (hasattr(scr, "history") and
                        scr.history.position < scr.history.size)
            moved = self._scroll(up=True)
            if moved or scrolled:
                event.stop(); event.prevent_default()
                return
            # 라이브 최하단 + 스크롤백 없음 → vim/less 등 원격 앱으로 전달
        if key == "pagedown" and self._scroll(up=False):
            event.stop(); event.prevent_default()
            return

        data = _KEY_SEQ.get(key)
        if data is None:
            if key.startswith("ctrl+") and len(key) == len("ctrl+x"):
                letter = key[-1]
                if "a" <= letter <= "z":
                    data = bytes([ord(letter) - ord("a") + 1])
            if data is None and event.character is not None:
                data = event.character.encode("utf-8")

        if data is not None:
            event.stop()
            event.prevent_default()
            try:
                os.write(self._fd, data)
            except OSError:
                self.stop()

    def on_paste(self, event):
        # 클립보드 붙여넣기(여러 줄 포함)는 Key 가 아니라 Paste 이벤트로 온다.
        if not self.active or self._fd is None:
            return
        text = getattr(event, "text", "")
        if not text:
            return
        event.stop()
        event.prevent_default()
        payload = text.encode("utf-8")
        try:
            while payload:
                n = os.write(self._fd, payload)
                payload = payload[n:]
        except OSError:
            self.stop()

