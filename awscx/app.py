"""메인 TUI 애플리케이션 및 진입점(main)."""
import argparse
import json
import os
import shutil
import subprocess
import sys
import time

import boto3
from botocore.exceptions import BotoCoreError, ClientError, NoCredentialsError
from rich.markup import escape as _escape
from rich.syntax import Syntax
from rich.text import Text
from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Horizontal, Vertical, VerticalScroll
from textual.widgets import DataTable, Footer, Input, Static
from textual.worker import get_current_worker
from textual import work

from .config import (
    __version__, version_label, DEFAULT_REGION, DEFAULT_COMMAND, CONFIG_PATH, DEFAULT_CONFIG,
    OUTPUT_PLACEHOLDER, PROFILE_PLACEHOLDER, MODE_PLACEHOLDER, EC2_PLACEHOLDER,
    log, load_config, save_config, setup_logging,
)
from .awsapi import (
    short_arn, list_clusters, list_services, list_running_tasks, list_ec2_instances,
    describe_service, describe_services_map, get_task_definition, awslogs_config,
    service_metric, service_metric_series, insights_metric_series,
    service_autoscaling, cluster_autoscaling_map, service_lb_health, target_group_health,
)
from .render import log_level_style, humanize_bytes, sparkline, _SPARK_BLOCKS
from .terminal import TerminalPane
from .screens import ConfigEditScreen


class AwsExecApp(App):
    CSS = """
    #topbar { dock: top; height: 1; background: $accent; }
    #breadcrumb {
        width: 1fr; color: $text; content-align: left middle; padding: 0 1;
    }
    #version {
        width: auto; color: $text; content-align: right middle; padding: 0 1;
    }
    #filter { dock: top; height: 1; display: none; }
    #filter.visible { display: block; }
    #status { dock: bottom; height: 1; color: $text-muted; padding: 0 1; }

    #body { height: 16; }
    #list { width: 1fr; }
    DataTable { height: 1fr; }
    #monitor {
        width: 45%; display: none; border-left: solid $accent; padding: 0 1;
    }
    #monitor.visible { display: block; }
    #monitor-title { height: 1; color: $accent; }
    #right { height: 1fr; border-top: solid $accent; }
    #output-title {
        dock: top; height: 1; background: $panel; color: $text; padding: 0 1;
    }
    #output-scroll { height: 1fr; padding: 0 1; }
    #terminal { height: 1fr; display: none; }
    #terminal.visible { display: block; }
    #output-scroll.hidden { display: none; }
    """

    BINDINGS = [
        Binding("enter", "status", "상태", show=True),
        Binding("s", "shell", "접속", show=True),
        Binding("l", "logs", "로그", show=True),
        Binding("f", "follow", "tail", show=True),
        Binding("t", "taskdef", "taskdef", show=True),
        Binding("p", "profiles", "프로파일", show=True),
        Binding("g", "config", "설정", show=True),
        Binding("escape,backspace", "back", "뒤로", show=True),
        Binding("slash", "filter", "필터", show=True),
        Binding("r", "refresh", "새로고침", show=True),
        Binding("c", "copy", "복사", show=True),
        Binding("ctrl+d", "console_down", "결과▼", show=True),
        Binding("ctrl+u", "console_up", "결과▲", show=True),
        Binding("j", "cursor_down", "아래", show=False),
        Binding("k", "cursor_up", "위", show=False),
        Binding("q,ctrl+c", "quit", "종료", show=True),
    ]

    def __init__(self, profile, config):
        super().__init__()
        self.config = config
        self.profile = profile
        self.region = config["region"]
        self.command = config["command"]
        self.ecs = None

        self.level = "targets"       # profiles | mode | targets | tasks | containers | ec2
        self.mode = "ecs"            # ecs | ec2
        self.items = []              # 현재 화면 데이터
        self.targets = []            # 전체 cluster/service (필터 원본)
        self.instances = []          # EC2 인스턴스 목록
        self.profiles = []           # 사용 가능한 profile 목록
        self.filter_text = ""
        self.cluster = None          # dict
        self.service = None          # str or None
        self.cur_task = None         # dict
        self.pending_action = None   # shell | logs (task/container 선택 후 수행할 동작)
        self.session = None          # boto3 Session (logs client 생성용)
        self.output_active = False   # 오른쪽 결과 패널에 내용이 표시 중인지
        self._monitor_timer = None   # 실시간 모니터 자동갱신 타이머
        self._monitoring = False
        self._monitor_cluster = None
        self._monitor_service = None
        self._monitor_debounce = None  # 커서 이동 디바운스 타이머
        self._pending_monitor = None
        self._num_buffer = ""          # profile 번호 입력 버퍼
        self._num_timer = None
        self._log_lines = []           # 로그 뷰의 원본 줄들(검색 필터용)
        self._log_title = ""
        self._log_search = ""
        self._output_is_logs = False   # 오른쪽 패널이 로그 뷰인지
        self._filter_mode = None       # 필터 입력이 대상: targets|profiles|logs
        self._output_plain = ""        # 현재 결과 패널 내용(클립보드 복사용)
        self._log_follow = False       # tail -f 모드
        self._log_follow_timer = None
        self._log_last_ts = 0          # 마지막으로 본 로그 타임스탬프(ms)
        self._log_cfg = None           # 로그 재조회용 awslogs 설정
        self._log_container = None
        self._config_edit_key = None   # 설정 편집 중인 키

    def compose(self) -> ComposeResult:
        with Horizontal(id="topbar"):
            yield Static(id="breadcrumb")
            yield Static(f"awscx {version_label()}", id="version")
        yield Input(placeholder="필터... (Esc 로 닫기)", id="filter")
        with Horizontal(id="body"):
            yield DataTable(cursor_type="row", zebra_stripes=True, id="list")
            with Vertical(id="monitor"):
                yield Static("", id="monitor-title")
                yield Static("", id="monitor-body", markup=False)
        with Vertical(id="right"):
            yield Static("실행 결과", id="output-title")
            with VerticalScroll(id="output-scroll"):
                yield Static(OUTPUT_PLACEHOLDER, id="output", expand=True, markup=False)
            yield TerminalPane(id="terminal")
        yield Static(id="status")
        yield Footer()

    def on_mount(self):
        self._apply_list_height()
        self.query_one("#terminal", TerminalPane)._color = bool(
            self.config.get("terminal_color", True))
        # 결과 콘솔(스크롤 컨테이너)이 클릭으로 포커스를 가져가지 않게 → 목록 포커스 유지
        # (스크롤은 Ctrl+U/D 로 가능)
        try:
            self.query_one("#output-scroll").can_focus = False
        except Exception:
            pass
        try:
            self.profiles = sorted(boto3.Session().available_profiles)
        except (BotoCoreError, ClientError):
            self.profiles = []
        if self.profile:
            self.set_profile(self.profile)
        else:
            self._populate_profiles()

    def _apply_list_height(self):
        """config 의 list_rows 로 목록 높이 조절(헤더 포함). CSS 대신 런타임 적용."""
        try:
            rows = int(self.config.get("list_rows", 15))
        except (TypeError, ValueError):
            rows = 15
        rows = max(3, min(80, rows))
        # 위쪽 목록 영역(#body) 높이를 고정 → 아래 콘솔(#right, 1fr)이 나머지를 차지
        self.query_one("#body").styles.height = rows + 1

    # ---- UI helpers -----------------------------------------------------
    def set_status(self, text):
        self.query_one("#status", Static).update(text)

    def set_breadcrumb(self):
        prof = self.profile or "(profile 미선택)"
        parts = [f"[b]{prof}[/b] @ {self.region}"]
        if self.level == "profiles":
            parts.append("profile 선택")
        elif self.level == "mode":
            parts.append("ECS / EC2 선택")
        elif self.level == "config":
            parts.append("설정 (config)")
        elif self.level == "ec2":
            parts.append("EC2 (SSM)")
        elif self.level == "targets":
            parts.append("cluster/service")
        elif self.cluster:
            parts.append(f"{self.cluster['clusterName']}/{self.service or ''}")
        if self.cur_task:
            parts.append(short_arn(self.cur_task["taskArn"]))
        self.query_one("#breadcrumb", Static).update("  ▸  ".join(parts))

    def _reset_table(self, columns):
        table = self.query_one(DataTable)
        table.clear(columns=True)
        table.add_columns(*columns)
        return table

    def show_error(self, msg):
        self.set_status(f"[red]ERROR:[/red] {_escape(str(msg))}")

    def show_output(self, title, renderable):
        """오른쪽 패널에 실행 결과를 표시한다."""
        self.query_one("#output-title", Static).update(title)
        self.query_one("#output", Static).update(renderable)
        self.query_one("#output-scroll", VerticalScroll).scroll_home(animate=False)
        self.output_active = True
        self._output_is_logs = False  # 로그 뷰면 _render_log 가 이후에 True 로 되돌림
        # 클립보드 복사용 평문 보관
        if isinstance(renderable, str):
            self._output_plain = renderable
        elif isinstance(renderable, Text):
            self._output_plain = renderable.plain
        else:
            self._output_plain = getattr(renderable, "code", None) or str(renderable)

    def clear_output(self):
        """오른쪽 결과 패널을 현재 레벨에 맞는 안내문으로 되돌린다."""
        self._stop_follow()
        placeholder = {
            "profiles": PROFILE_PLACEHOLDER,
            "mode": MODE_PLACEHOLDER,
            "ec2": EC2_PLACEHOLDER,
            "config": "설정 화면 · Enter=편집 · Esc=뒤로",
        }.get(self.level, OUTPUT_PLACEHOLDER)
        self.query_one("#output-title", Static).update("실행 결과")
        self.query_one("#output", Static).update(placeholder)
        self.output_active = False
        self._output_is_logs = False

    # ---- profile 선택/변경 ---------------------------------------------
    def action_profiles(self):
        self._populate_profiles()

    def _populate_profiles(self):
        self.level = "profiles"
        self.cluster = self.service = self.cur_task = None
        self.pending_action = None
        self.filter_text = ""
        self._stop_monitor()
        self._close_filter()
        self.clear_output()
        self.set_breadcrumb()
        self._render_profiles()

    def _render_profiles(self):
        f = self.filter_text.lower()
        self.items = [p for p in self.profiles if f in p.lower()]
        table = self._reset_table(("#", "PROFILE"))
        for i, p in enumerate(self.items, 1):
            name = f"{p}   [dim](현재)[/dim]" if p == self.profile else p
            table.add_row(str(i), name)
        table.focus()
        if not self.profiles:
            self.set_status("사용 가능한 profile 이 없습니다. (~/.aws/config 확인)")
        else:
            suffix = f" (필터: {_escape(self.filter_text)})" if self.filter_text else ""
            self.set_status(
                f"{len(self.items)}개 profile · 번호 입력/Enter=선택 · /=필터{suffix}")

    def set_profile(self, name):
        self.profile = name
        try:
            self.session = boto3.Session(profile_name=name, region_name=self.region)
            self.ecs = self.session.client("ecs")
        except (BotoCoreError, ClientError) as e:
            self.show_error(f"'{name}' 세션 생성 실패: {e}")
            return
        self.filter_text = ""
        self._populate_mode()

    # ---- ECS / EC2 선택 -------------------------------------------------
    MODE_ITEMS = [
        {"key": "ecs", "label": "ECS  (cluster/service exec)"},
        {"key": "ec2", "label": "EC2  (SSM session)"},
    ]

    def _populate_mode(self):
        self.level = "mode"
        self.cluster = self.service = self.cur_task = None
        self.pending_action = None
        self._stop_monitor()
        self._close_filter()
        self.clear_output()
        self.items = list(self.MODE_ITEMS)
        self.set_breadcrumb()
        table = self._reset_table(("#", "MODE"))
        for i, m in enumerate(self.items, 1):
            table.add_row(str(i), m["label"])
        table.focus()
        self.set_status("접근 대상 선택 · 번호/Enter=선택 · Esc=프로파일")

    # ---- 번호 입력: profiles/mode=선택, targets/ec2=커서 이동 (2자리) --
    _NUM_LEVELS = ("profiles", "mode", "targets", "ec2", "config")

    def on_key(self, event):
        if len(self.screen_stack) > 1:
            return  # 모달(설정 편집 팝업 등)이 떠 있으면 번호 입력 처리 안 함
        if self.level not in self._NUM_LEVELS:
            return
        if self.query_one("#filter").has_class("visible"):
            return
        ch = event.character
        if not ch or not ch.isdigit():
            return
        event.stop()
        event.prevent_default()
        self._num_buffer += ch
        n = int(self._num_buffer)
        # 더 긴 번호가 불가능하면 즉시 확정, 아니면 잠깐 대기(2자리 입력 여지)
        if n == 0 or n * 10 > len(self.items):
            self._commit_num()
        else:
            if self._num_timer is not None:
                self._num_timer.stop()
            self._num_timer = self.set_timer(0.5, self._commit_num)

    def _commit_num(self):
        if self._num_timer is not None:
            self._num_timer.stop()
            self._num_timer = None
        buf, self._num_buffer = self._num_buffer, ""
        if not buf or self.level not in self._NUM_LEVELS:
            return
        n = int(buf)
        if not (1 <= n <= len(self.items)):
            self.set_status(f"{n} 번은 없습니다 (1~{len(self.items)})")
            return
        if self.level == "profiles":
            self.set_profile(self.items[n - 1])
        elif self.level == "mode":
            self._select_mode(self.items[n - 1]["key"])
        else:  # targets/ec2: 해당 행으로 커서 이동
            self.query_one(DataTable).move_cursor(row=n - 1)

    def _select_mode(self, key):
        self.mode = key
        if key == "ecs":
            self.load_targets()
        else:
            self.load_ec2()

    # ---- targets (cluster/service flat 목록) ---------------------------
    @work(exclusive=True, thread=True)
    def load_targets(self):
        worker = get_current_worker()
        self.call_from_thread(self.set_status, "cluster 조회 중...")
        try:
            clusters = list_clusters(self.ecs)
        except NoCredentialsError:
            self.call_from_thread(
                self._show_load_error,
                "자격증명 없음",
                f"'{self.profile}' 프로파일의 자격증명을 찾을 수 없습니다.\n\n"
                f"SSO 프로파일이면 로그인하세요:\n"
                f"  aws sso login --profile {self.profile}\n\n"
                f"아니면 ~/.aws/credentials 를 확인하세요.")
            return
        except (BotoCoreError, ClientError) as e:
            self.call_from_thread(
                self._show_load_error,
                "cluster 조회 실패",
                f"{e}\n\n"
                f"SSO 프로파일이면 로그인이 만료됐을 수 있습니다:\n"
                f"  aws sso login --profile {self.profile}")
            return

        try:
            elbv2 = self.session.client("elbv2", region_name=self.region)
        except (BotoCoreError, ClientError):
            elbv2 = None
        try:
            aas = self.session.client("application-autoscaling", region_name=self.region)
        except (BotoCoreError, ClientError):
            aas = None

        targets = []
        for i, c in enumerate(clusters, 1):
            if worker.is_cancelled:
                return
            self.call_from_thread(
                self.set_status, f"service 조회 중... ({i}/{len(clusters)} {c['clusterName']})")
            try:
                services = list_services(self.ecs, c["clusterArn"])
            except (BotoCoreError, ClientError):
                services = []
            detail = {}
            scale_map = {}
            if services:
                try:
                    detail = describe_services_map(self.ecs, c["clusterArn"], services)
                except (BotoCoreError, ClientError):
                    detail = {}
                if aas is not None:
                    try:
                        scale_map = cluster_autoscaling_map(aas, c["clusterName"], services)
                    except (BotoCoreError, ClientError):
                        scale_map = {}
            if services:
                for s in services:
                    d = detail.get(s, {})
                    lb = None
                    if elbv2 is not None and d.get("loadBalancers"):
                        try:
                            lb = service_lb_health(elbv2, d)
                        except (BotoCoreError, ClientError):
                            lb = None
                    targets.append({
                        "cluster": c, "service": s,
                        "status": d.get("status", "-"),
                        "running": d.get("runningCount"),
                        "desired": d.get("desiredCount"),
                        "exec": d.get("enableExecuteCommand"),
                        "lb": lb,
                        "scale": scale_map.get(s),
                    })
            else:
                targets.append({
                    "cluster": c, "service": None,
                    "status": c.get("status", "-"),
                    "running": None, "desired": None, "exec": None,
                    "lb": None, "scale": None,
                })
        if worker.is_cancelled:
            return
        self.call_from_thread(self._populate_targets, targets)

    def _populate_targets(self, targets):
        self.level = "targets"
        self.cluster = self.service = self.cur_task = None
        self.targets = targets
        self.filter_text = ""
        self._stop_monitor()
        self._close_filter()
        self.clear_output()
        self.set_breadcrumb()
        self._render_targets()

    def _render_targets(self):
        f = self.filter_text.lower()
        self.items = [
            t for t in self.targets
            if f in f"{t['cluster']['clusterName']}/{t['service'] or ''}".lower()
        ]
        table = self._reset_table(
            ("#", "CLUSTER / SERVICE", "STATUS", "EXEC", "TASKS", "LB", "SCALE"))
        for i, t in enumerate(self.items, 1):
            name = t["service"] or "[dim]<service 없음>[/dim]"
            if t["service"] is None:
                exec_cell = tasks_cell = lb_cell = scale_cell = "-"
            else:
                exec_cell = "[green]ON[/green]" if t.get("exec") else "[red]OFF[/red]"
                r, d = t.get("running"), t.get("desired")
                tasks_cell = f"{r}/{d}" if r is not None else "-"
                lb = t.get("lb")
                if lb is None:
                    lb_cell = "[dim]-[/dim]"
                else:
                    h, tot = lb
                    color = "green" if (tot > 0 and h == tot) else "red"
                    lb_cell = f"[{color}]{h}/{tot}[/{color}]"
                sc = t.get("scale")
                scale_cell = f"[cyan]{sc[0]}~{sc[1]}[/cyan]" if sc else "[dim]고정[/dim]"
            table.add_row(
                str(i), f"{t['cluster']['clusterName']}/{name}",
                t.get("status") or "-", exec_cell, tasks_cell, lb_cell, scale_cell)
        table.focus()
        if not self.targets:
            self.show_output(
                f"'{self.profile}' - 항목 없음",
                f"'{self.profile}' @ {self.region} 에서 접근 가능한\n"
                f"ECS cluster/service 가 없습니다.\n\n"
                f"- 리전이 맞는지 확인하세요 (-r 옵션)\n"
                f"- 다른 프로파일을 선택하려면 p 를 누르세요")
            self.set_status("접근 가능한 ECS cluster/service 없음 · p=프로파일")
            return
        suffix = f" (필터: {_escape(self.filter_text)})" if self.filter_text else ""
        self.set_status(
            f"{len(self.items)}개 · 번호=이동 · Enter=상태 · s=접속 · l=로그 · t=taskdef · /=필터{suffix}")

    def _show_load_error(self, title, message):
        """cluster 조회 실패/자격증명 오류를 오른쪽 패널에 크게 표시."""
        self.show_output(title, message)
        self.set_status(f"[red]{_escape(title)}[/red] · p=프로파일 변경 · r=재시도")

    # ---- 설정(config) 화면 (g) -----------------------------------------
    SETTINGS = [
        {"key": "region", "label": "AWS region", "type": "str"},
        {"key": "command", "label": "shell command", "type": "str"},
        {"key": "log_enabled", "label": "로그 기록", "type": "bool"},
        {"key": "log_level", "label": "로그 레벨(DEBUG/INFO/WARNING/ERROR)", "type": "str"},
        {"key": "log_path", "label": "로그 파일 경로", "type": "str"},
        {"key": "monitor_interval_sec", "label": "모니터 갱신주기(초)", "type": "int"},
        {"key": "tail_interval_sec", "label": "로그 tail 주기(초)", "type": "int"},
        {"key": "list_rows", "label": "목록에 보이는 줄 수", "type": "int"},
        {"key": "terminal_color", "label": "터미널 컬러 렌더링", "type": "bool"},
        {"key": "mouse",
         "label": "마우스 사용 (켜면 휠 스크롤/클릭·선택은 Option+드래그 / 끄면 네이티브 드래그 선택·휠은 PageUp키 / 재시작 필요)",
         "type": "bool"},
    ]

    def action_config(self):
        self._populate_config()

    def _populate_config(self):
        self.level = "config"
        self._stop_monitor()
        self._stop_follow()
        self._close_filter()
        self.items = list(self.SETTINGS)
        self.set_breadcrumb()
        self._render_config()
        self.show_output(
            "설정",
            f"파일: {CONFIG_PATH}\n\n"
            "- Enter: 값 편집(부울은 토글)\n"
            "- 변경 즉시 저장됩니다\n"
            "- region/log 경로 등 일부는 다음 실행/재선택부터 적용")

    def _render_config(self):
        def fmt(v, typ):
            if typ == "bool":
                return "ON" if v else "OFF"
            return str(v)

        table = self._reset_table(("#", "SETTING", "VALUE", "DEFAULT"))
        for i, sett in enumerate(self.items, 1):
            key, typ = sett["key"], sett["type"]
            val = self.config.get(key)
            default = DEFAULT_CONFIG.get(key)
            if typ == "bool":
                vcell = "[green]ON[/green]" if val else "[red]OFF[/red]"
            else:
                vcell = str(val)
            # 기본값과 다르면 강조 표시
            same = (val == default)
            dcell = f"[dim]{fmt(default, typ)}[/dim]"
            if not same:
                vcell = f"[b]{vcell}[/b]" if typ != "bool" else vcell
            table.add_row(str(i), sett["label"], vcell, dcell)
        table.focus()
        self.set_status("설정 · 번호=이동 · Enter=편집/토글 · Esc=뒤로 (DEFAULT=기본값)")

    def _edit_setting(self, idx):
        sett = self.items[idx]
        key, typ = sett["key"], sett["type"]
        if typ == "bool":
            self.config[key] = not self.config.get(key)
            self._apply_config_change(key)
            self._render_config()
            return
        # str/int → 해당 항목 전용 팝업으로 편집
        self._config_edit_key = key
        self.push_screen(
            ConfigEditScreen(sett["label"], self.config.get(key, "")),
            self._on_config_edit_done)

    def _on_config_edit_done(self, value):
        if value is not None:
            self._commit_config_edit(value)

    def _commit_config_edit(self, value):
        key = getattr(self, "_config_edit_key", None)
        self._config_edit_key = None
        if not key:
            return
        typ = next((s["type"] for s in self.SETTINGS if s["key"] == key), "str")
        if typ == "int":
            try:
                self.config[key] = int(value)
            except ValueError:
                self.set_status(f"[red]숫자를 입력하세요[/red]")
                return
        else:
            self.config[key] = value.strip()
        self._apply_config_change(key)
        self._render_config()

    def _apply_config_change(self, key):
        save_config(self.config)
        if key in ("log_enabled", "log_level", "log_path"):
            setup_logging(self.config)
        if key == "command":
            self.command = self.config["command"]
        if key == "region":
            self.region = self.config["region"]
            self.set_breadcrumb()
        if key == "list_rows":
            self._apply_list_height()
        if key == "terminal_color":
            self.query_one("#terminal", TerminalPane)._color = bool(
                self.config.get("terminal_color", True))
        if key == "mouse":
            self.set_status("'mouse' 저장됨 · 재시작 후 적용됩니다")
            return
        self.set_status(f"'{key}' 저장됨 → {CONFIG_PATH}")

    # ---- EC2 (SSM) 목록 ------------------------------------------------
    @work(exclusive=True, thread=True)
    def load_ec2(self):
        worker = get_current_worker()
        self.call_from_thread(self.set_status, "EC2 인스턴스 조회 중...")
        try:
            ec2 = self.session.client("ec2", region_name=self.region)
            ssm = self.session.client("ssm", region_name=self.region)
            instances = list_ec2_instances(ec2, ssm)
        except NoCredentialsError:
            self.call_from_thread(
                self._show_load_error, "자격증명 없음",
                f"'{self.profile}' 자격증명 없음.\n  aws sso login --profile {self.profile}")
            return
        except (BotoCoreError, ClientError) as e:
            self.call_from_thread(
                self._show_load_error, "EC2 조회 실패", f"{type(e).__name__}: {e}")
            return
        if worker.is_cancelled:
            return
        self.call_from_thread(self._populate_ec2, instances)

    def _populate_ec2(self, instances):
        self.level = "ec2"
        self._stop_monitor()
        self._stop_follow()
        self.instances = instances
        self.items = instances
        self.clear_output()   # EC2 안내문으로 오른쪽 패널 초기화
        self.set_breadcrumb()
        table = self._reset_table(
            ("#", "INSTANCE", "NAME", "STATE", "SSM", "TYPE", "PRIVATE IP"))
        for i, ins in enumerate(instances, 1):
            ping = ins.get("ssm")
            if ping == "Online":
                ssm_cell = "[green]online[/green]"
            elif ping:
                ssm_cell = f"[yellow]{ping}[/yellow]"
            else:
                ssm_cell = "[red]none[/red]"
            table.add_row(
                str(i), ins["id"], ins.get("name") or "-",
                ins.get("state", ""), ssm_cell, ins.get("type", ""), ins.get("ip", "-"))
        table.focus()
        if not instances:
            self.show_output(
                "EC2 없음",
                f"'{self.profile}' @ {self.region} 에 running EC2 인스턴스가 없습니다.\n"
                f"- 리전 확인 (-r), 다른 프로파일은 p")
            self.set_status("running EC2 없음 · Esc=뒤로 · p=프로파일")
        else:
            self.set_status(
                f"{len(instances)}개 · 번호=이동 · Enter=SSM 접속 · Esc=뒤로 "
                "(SSM online 만 접속 가능)")

    def ssm_connect(self, instance):
        if instance.get("ssm") != "Online":
            self.set_status(
                f"[yellow]{instance['id']} 는 SSM online 이 아닙니다 "
                f"(SSM Agent/IAM/네트워크 확인)[/yellow]")
            return
        cmd = [
            "aws", "ssm", "start-session",
            "--target", instance["id"],
            "--profile", self.profile,
            "--region", self.region,
        ]
        title = f"SSM  {instance['id']}" + (
            f"  ({instance['name']})" if instance.get("name") else "")
        self._run_in_terminal(cmd, title)

    # ---- task / container 선택 화면 -----------------------------------
    def _populate_tasks(self, tasks):
        self.level = "tasks"
        self._stop_monitor()
        self.cur_task = None
        self.items = tasks
        self.set_breadcrumb()
        table = self._reset_table(("TASK", "EXEC", "GROUP", "CONTAINERS", "STATUS"))
        for t in tasks:
            containers = ",".join(c["name"] for c in t.get("containers", []))
            table.add_row(
                short_arn(t["taskArn"]),
                "ON" if t.get("enableExecuteCommand") else "OFF",
                t.get("group", ""), containers, t.get("lastStatus", ""),
            )
        table.focus()
        self.set_status(f"running task {len(tasks)}개 · Enter=선택 · Esc=뒤로")

    def _populate_containers(self, task):
        self.level = "containers"
        self._stop_monitor()
        self.cur_task = task
        self.items = task.get("containers", [])
        self.set_breadcrumb()
        table = self._reset_table(("CONTAINER", "STATUS", "IMAGE"))
        for c in self.items:
            table.add_row(
                c["name"], c.get("lastStatus", ""),
                (c.get("image", "") or "").rsplit("/", 1)[-1],
            )
        table.focus()
        exec_on = task.get("enableExecuteCommand")
        warn = "" if (exec_on or self.pending_action != "shell") \
            else "  [yellow](exec OFF - 접속 실패할 수 있음)[/yellow]"
        self.set_status(f"container 선택 후 Enter · Esc=뒤로{warn}")

    # ---- cursor / target 읽기 -----------------------------------------
    def _cursor_index(self):
        idx = self.query_one(DataTable).cursor_row
        if idx is None or idx < 0 or idx >= len(self.items):
            return None
        return idx

    def _set_target_from_cursor(self):
        """targets 레벨에서 커서 위치의 cluster/service 를 상태에 반영."""
        idx = self._cursor_index()
        if idx is None:
            return False
        t = self.items[idx]
        self.cluster = t["cluster"]
        self.service = t["service"]
        return True

    # ---- Enter (레벨별 동작) ------------------------------------------
    def action_status(self):
        if self.level == "profiles":
            idx = self._cursor_index()
            if idx is not None:
                self.set_profile(self.items[idx])
        elif self.level == "mode":
            idx = self._cursor_index()
            if idx is not None:
                self._select_mode(self.items[idx]["key"])
        elif self.level == "ec2":
            idx = self._cursor_index()
            if idx is not None:
                self.ssm_connect(self.items[idx])
        elif self.level == "config":
            idx = self._cursor_index()
            if idx is not None:
                self._edit_setting(idx)
        elif self.level == "targets":
            if self._set_target_from_cursor():
                self._stop_follow()
                self.show_status()
        elif self.level == "tasks":
            idx = self._cursor_index()
            if idx is not None:
                self._after_task_selected(self.items[idx])
        elif self.level == "containers":
            idx = self._cursor_index()
            if idx is not None:
                self._perform(self.cur_task, self.items[idx]["name"])

    def on_data_table_row_selected(self):
        self.action_status()

    def on_data_table_row_highlighted(self, event):
        # targets 레벨에서 커서가 서비스 행에 놓이면 자동으로 모니터 시작
        if self.level != "targets":
            return
        self._stop_follow()  # 다른 서비스로 이동하면 로그 tail 중지
        idx = event.cursor_row
        if idx is None or idx < 0 or idx >= len(self.items):
            return
        item = self.items[idx]
        if not item["service"]:
            self._stop_monitor()          # service 없는 행이면 모니터 숨김
            return
        self._pending_monitor = (item["cluster"], item["service"])
        if self._monitor_debounce is not None:
            self._monitor_debounce.stop()
        self._monitor_debounce = self.set_timer(0.4, self._apply_pending_monitor)

    def _apply_pending_monitor(self):
        self._monitor_debounce = None
        if self.level != "targets" or not self._pending_monitor:
            return
        cluster, service = self._pending_monitor
        self._monitor_cluster = cluster
        self._monitor_service = service
        self._start_monitor()

    # ---- s / l / t (targets 레벨 전용) --------------------------------
    def action_shell(self):
        if self.level == "targets" and self._set_target_from_cursor():
            self.pending_action = "shell"
            self.resolve_tasks_then_pick()

    def action_logs(self):
        if self.level == "targets" and self._set_target_from_cursor():
            self.pending_action = "logs"
            self.resolve_tasks_then_pick()

    def action_taskdef(self):
        if self.level == "targets" and self._set_target_from_cursor():
            self._stop_follow()
            self.show_taskdef()

    # ---- 실시간 모니터: 서비스 선택 시 자동, 30초마다 갱신 -------------
    def _start_monitor(self):
        self._stop_monitor()
        self._monitoring = True
        self.query_one("#monitor").add_class("visible")
        self.query_one("#monitor-title", Static).update(
            f"모니터  {self._monitor_cluster['clusterName']}/{self._monitor_service}  (Esc 중지)")
        self.query_one("#monitor-body", Static).update("조회 중...")
        self.refresh_monitor()
        self._monitor_timer = self.set_interval(
            max(5, int(self.config.get("monitor_interval_sec", 30))), self.refresh_monitor)

    def _stop_monitor(self):
        self._monitoring = False
        self._pending_monitor = None
        if self._monitor_debounce is not None:
            self._monitor_debounce.stop()
            self._monitor_debounce = None
        if self._monitor_timer is not None:
            self._monitor_timer.stop()
            self._monitor_timer = None
        try:
            self.query_one("#monitor").remove_class("visible")
        except Exception:
            pass

    def refresh_monitor(self):
        if self._monitoring:
            self._monitor_worker()

    @work(exclusive=True, thread=True)
    def _monitor_worker(self):
        if not self._monitoring:
            return
        cluster = self._monitor_cluster
        service = self._monitor_service
        cname = cluster["clusterName"]
        try:
            cw = self.session.client("cloudwatch", region_name=self.region)
            cpu = service_metric_series(cw, "CPUUtilization", cname, service)
            mem = service_metric_series(cw, "MemoryUtilization", cname, service)
            # network / disk 는 Container Insights 필요
            net_rx = insights_metric_series(cw, "NetworkRxBytes", cname, service)
            net_tx = insights_metric_series(cw, "NetworkTxBytes", cname, service)
            disk_r = insights_metric_series(cw, "StorageReadBytes", cname, service)
            disk_w = insights_metric_series(cw, "StorageWriteBytes", cname, service)
        except Exception as e:
            self.call_from_thread(
                self._set_monitor_body, f"(조회 실패: {type(e).__name__}: {e})")
            return
        if not self._monitoring:
            return

        def pct_color(v):
            return "bold red" if v >= 85 else "yellow" if v >= 60 else "green"

        def pct_row(body, label, vals):
            body.append(f"{label:7s}", style="bold cyan")
            if not vals:
                body.append("(데이터 없음)\n", style="dim")
                return
            now, avg, peak = vals[-1], sum(vals) / len(vals), max(vals)
            body.append("now=")
            body.append(f"{now:5.1f}%", style=pct_color(now))
            body.append(f"  avg={avg:5.1f}%  peak=")
            body.append(f"{peak:5.1f}%", style=pct_color(peak))
            body.append("  ")
            hi = max(vals) or 1
            last = len(_SPARK_BLOCKS) - 1
            for v in vals:
                idx = max(0, min(last, int(v / hi * last)))
                body.append(_SPARK_BLOCKS[idx], style=pct_color(v))
            body.append("\n")

        def rate_row(body, label, a_vals, b_vals, a_name, b_name):
            body.append(f"{label:7s}", style="bold cyan")
            if not (a_vals or b_vals):
                body.append("(데이터 없음 · Container Insights 필요)\n", style="dim")
                return

            def cur(v):
                return humanize_bytes(v[-1] / 60) + "/s" if v else "-"

            def pk(v):
                return humanize_bytes(max(v) / 60) + "/s" if v else "-"
            body.append(f"{a_name} ", style="bold")
            body.append(f"now={cur(a_vals)} ", style="green")
            body.append(f"peak={pk(a_vals)}   ", style="magenta")
            body.append(f"{b_name} ", style="bold")
            body.append(f"now={cur(b_vals)} ", style="green")
            body.append(f"peak={pk(b_vals)}\n", style="magenta")

        stamp = time.strftime("%H:%M:%S", time.localtime())
        body = Text()
        pct_row(body, "CPU", cpu)
        pct_row(body, "Memory", mem)
        rate_row(body, "Network", net_rx, net_tx, "Rx", "Tx")
        rate_row(body, "Disk", disk_r, disk_w, "R", "W")
        body.append(f"최근 1시간·1분 간격 · 갱신 {stamp} · Esc 중지", style="dim")
        self.call_from_thread(self._set_monitor_body, body)
        self.call_from_thread(
            self.set_status, f"실시간 모니터 (30초 갱신, 최근 {stamp}) · Esc 중지")

    def _set_monitor_body(self, text):
        self.query_one("#monitor-body", Static).update(text)

    # ---- shell/logs 공통: running task 조회 후 자동/선택 ---------------
    @work(exclusive=True, thread=True)
    def resolve_tasks_then_pick(self):
        worker = get_current_worker()
        self.call_from_thread(self.set_status, "running task 조회 중...")
        try:
            tasks = list_running_tasks(self.ecs, self.cluster["clusterArn"], self.service)
        except (BotoCoreError, ClientError) as e:
            self.call_from_thread(self.show_error, str(e))
            return
        if worker.is_cancelled:
            return
        if not tasks:
            self.call_from_thread(self.show_error, "RUNNING task 가 없습니다.")
            return
        if len(tasks) == 1:
            self.call_from_thread(self._after_task_selected, tasks[0])
        else:
            self.call_from_thread(self._populate_tasks, tasks)

    def _after_task_selected(self, task):
        containers = task.get("containers", [])
        if len(containers) == 1:
            self._perform(task, containers[0]["name"])
        else:
            self._populate_containers(task)

    def _perform(self, task, container_name):
        log.info("_perform action=%s task=%s container=%s",
                 self.pending_action, short_arn(task.get("taskArn", "")), container_name)
        if self.pending_action == "shell":
            self.exec_into(task, container_name)
        elif self.pending_action == "logs":
            self.show_logs(task, container_name)

    # ---- 뒤로 / 새로고침 / 이동 ---------------------------------------
    def action_back(self):
        if self.query_one("#filter").has_class("visible"):
            self._close_filter()
            return
        # 임베드 터미널 세션이 떠 있으면 먼저 종료(_on_term_exit 로 복원)
        term = self.query_one("#terminal", TerminalPane)
        if term.active:
            term.stop()
            return
        # 오른쪽 결과(로그/상태/taskdef)가 떠 있으면 먼저 그것을 닫는다
        if self.output_active:
            self.clear_output()
            self.set_status("결과 닫음")
            self.query_one(DataTable).focus()
            return
        # 계층: profiles → mode → (targets|ec2) → tasks/containers
        if self.level == "profiles":
            if self.ecs is not None:
                self._populate_mode()
        elif self.level == "mode":
            self._populate_profiles()
        elif self.level == "config":
            # 설정에서 나가면 mode(또는 프로파일)로
            if self.ecs is not None:
                self._populate_mode()
            else:
                self._populate_profiles()
        elif self.level in ("targets", "ec2"):
            self._populate_mode()
        elif self.level in ("tasks", "containers"):
            self._reset_to_targets()

    def _reset_to_targets(self):
        self._stop_monitor()
        self.cluster = self.service = self.cur_task = None
        self.pending_action = None
        self.level = "targets"
        self.set_breadcrumb()
        self._render_targets()

    def action_refresh(self):
        if self.level == "targets":
            self.load_targets()
        elif self.level == "ec2":
            self.load_ec2()
        elif self.level in ("tasks", "containers"):
            self._reset_to_targets()

    def action_cursor_down(self):
        self.query_one(DataTable).action_cursor_down()

    def action_cursor_up(self):
        self.query_one(DataTable).action_cursor_up()

    def action_copy(self):
        # 현재 결과 콘솔 내용을 시스템 클립보드로 복사 (검색 필터 중이면 그 결과)
        if self._output_is_logs and self._log_lines:
            term = self._log_search.lower()
            src = ([ln for ln in self._log_lines if term in ln.lower()]
                   if term else self._log_lines)
            data = "\n".join(src)
        else:
            data = self._output_plain
        if not self.output_active or not data:
            self.set_status("복사할 결과가 없습니다.")
            return
        payload = data.encode("utf-8")
        try:
            if shutil.which("pbcopy"):
                subprocess.run(["pbcopy"], input=payload, check=False)
            elif shutil.which("wl-copy"):
                subprocess.run(["wl-copy"], input=payload, check=False)
            elif shutil.which("xclip"):
                subprocess.run(["xclip", "-selection", "clipboard"],
                               input=payload, check=False)
            else:
                path = os.path.abspath("awscx_output.txt")
                with open(path, "w", encoding="utf-8") as f:
                    f.write(data)
                self.set_status(f"클립보드 도구 없음 → 파일 저장: {path}")
                return
            self.set_status(f"클립보드에 복사했습니다 ({data.count(chr(10)) + 1}줄)")
        except Exception as e:
            self.show_error(f"복사 실패: {type(e).__name__}: {e}")

    def action_console_down(self):
        self.query_one("#output-scroll", VerticalScroll).scroll_page_down(animate=False)

    def action_console_up(self):
        self.query_one("#output-scroll", VerticalScroll).scroll_page_up(animate=False)

    # ---- 필터 -----------------------------------------------------------
    def action_filter(self):
        # 로그 뷰가 떠 있으면 로그 검색, 아니면 목록/프로필 필터
        if self.output_active and self._output_is_logs:
            self._filter_mode = "logs"
        elif self.level == "profiles":
            self._filter_mode = "profiles"
        elif self.level == "targets":
            self._filter_mode = "targets"
        else:
            return
        inp = self.query_one("#filter", Input)
        inp.value = self._log_search if self._filter_mode == "logs" else self.filter_text
        inp.add_class("visible")
        inp.focus()

    def _close_filter(self):
        inp = self.query_one("#filter", Input)
        inp.remove_class("visible")
        self._filter_mode = None
        self.query_one(DataTable).focus()

    def on_input_changed(self, event):
        if event.input.id != "filter":
            return
        if self._filter_mode == "logs":
            self._log_search = event.value
            self._render_log()
        elif self._filter_mode == "profiles":
            self.filter_text = event.value
            self._render_profiles()
        else:
            self.filter_text = event.value
            self._render_targets()

    def on_input_submitted(self, event):
        if event.input.id == "filter":
            self._close_filter()

    # ---- 상태 보기 (Enter) ---------------------------------------------
    @work(exclusive=True, thread=True)
    def show_status(self):
        worker = get_current_worker()
        cluster = self.cluster
        service = self.service
        self.call_from_thread(self.set_status, "상태 조회 중...")
        try:
            tasks = list_running_tasks(self.ecs, cluster["clusterArn"], service)
            svc = describe_service(self.ecs, cluster["clusterArn"], service) if service else None
        except Exception as e:
            self.call_from_thread(
                self._show_load_error, "상태 조회 실패", f"{type(e).__name__}: {e}")
            return
        if worker.is_cancelled:
            return

        try:
            body = Text()

            def st_style(status):
                s = (status or "").upper()
                if s in ("ACTIVE", "RUNNING", "PRIMARY", "COMPLETED", "HEALTHY"):
                    return "green"
                if s in ("DRAINING", "PENDING", "IN_PROGRESS", "PROVISIONING", "UNKNOWN"):
                    return "yellow"
                return "red"

            def pct_style(v):
                return "bold red" if v >= 85 else "yellow" if v >= 60 else "green"

            def sect(name):
                body.append(f"\n{name}\n", style="bold cyan")

            def kv(label, value, vstyle=""):
                body.append(f"{label}", style="dim")
                body.append(f"{value}\n", style=vstyle)

            body.append("Cluster : ", style="dim")
            body.append(cluster["clusterName"])
            body.append("  (")
            body.append(cluster["status"], style=st_style(cluster["status"]))
            body.append(")\n")

            if svc:
                body.append("Service : ", style="dim")
                body.append(svc["serviceName"])
                body.append("  (")
                body.append(svc["status"], style=st_style(svc["status"]))
                body.append(")\n")
                running, desired = svc.get("runningCount"), svc.get("desiredCount")
                body.append("Count   : ", style="dim")
                body.append(f"{running}/{desired}",
                            style="green" if running == desired else "yellow")
                body.append(f"  (desired={desired} running={running} "
                            f"pending={svc.get('pendingCount')})\n", style="dim")
                kv("LaunchType : ", svc.get("launchType", "-"))
                kv("TaskDef : ", short_arn(svc.get("taskDefinition", "")))

                sect("Deployments:")
                for d in svc.get("deployments", []):
                    body.append("  - ")
                    body.append(f"{d.get('status')}", style=st_style(d.get("status")))
                    body.append(f"  {short_arn(d.get('taskDefinition',''))}  "
                                f"desired={d.get('desiredCount')} running={d.get('runningCount')} ")
                    body.append(f"rollout={d.get('rolloutState','-')}\n",
                                style=st_style(d.get("rolloutState")))

                sect("Auto Scaling:")
                try:
                    aas = self.session.client("application-autoscaling",
                                              region_name=self.region)
                    asg = service_autoscaling(aas, cluster["clusterName"], service)
                    if not asg:
                        body.append("  미설정 (고정 desired count)\n", style="dim")
                    else:
                        body.append("  용량 : ", style="dim")
                        body.append(f"min={asg['min']}  max={asg['max']}\n", style="cyan")
                        if not asg["policies"]:
                            body.append("  (scalable target 만 있고 정책 없음)\n", style="dim")
                        for p in asg["policies"]:
                            ptype = p.get("PolicyType")
                            body.append(f"  - {p.get('PolicyName')} ")
                            if ptype == "TargetTrackingScaling":
                                cfg = p.get("TargetTrackingScalingPolicyConfiguration", {})
                                metric = (cfg.get("PredefinedMetricSpecification", {})
                                          .get("PredefinedMetricType", "custom"))
                                body.append(
                                    f"target={cfg.get('TargetValue')} {metric}\n", style="green")
                            else:
                                body.append(f"({ptype})\n", style="yellow")
                except Exception as e:
                    body.append(f"  (autoscaling 조회 실패: {type(e).__name__}: {e})\n",
                                style="red")

                sect("Monitoring (최근 1시간, CloudWatch):")
                try:
                    cw = self.session.client("cloudwatch", region_name=self.region)
                    cpu = service_metric(cw, "CPUUtilization", cluster["clusterName"], service)
                    mem = service_metric(cw, "MemoryUtilization", cluster["clusterName"], service)
                    for lbl, m in (("CPU   ", cpu), ("Memory", mem)):
                        body.append(f"  {lbl} : ", style="dim")
                        if not m:
                            body.append("(데이터 없음)\n", style="dim")
                            continue
                        body.append(f"avg={m['avg']:.1f}%", style=pct_style(m["avg"]))
                        body.append("  ")
                        body.append(f"peak={m['peak']:.1f}%\n", style=pct_style(m["peak"]))
                except Exception as e:
                    body.append(f"  (지표 조회 실패: {type(e).__name__}: {e})\n", style="red")

                sect("TaskDef 설정:")
                try:
                    td = get_task_definition(self.ecs, svc["taskDefinition"])
                    body.append("  Task 크기 : ", style="dim")
                    body.append(f"cpu={td.get('cpu', '-')} units  "
                                f"memory={td.get('memory', '-')} MB  "
                                f"network={td.get('networkMode', '-')}\n")
                    for cd in td.get("containerDefinitions", []):
                        ports = ", ".join(
                            (f"{p.get('containerPort')}"
                             + (f"->{p['hostPort']}" if p.get("hostPort") else "")
                             + (f"/{p['protocol']}" if p.get("protocol") else ""))
                            for p in cd.get("portMappings", [])) or "-"
                        ccpu = cd.get("cpu") or "-"
                        cmem = cd.get("memory") or cd.get("memoryReservation") or "-"
                        body.append("  - ")
                        body.append(cd["name"], style="bold")
                        body.append(f": cpu={ccpu}  mem={cmem}  ", style="dim")
                        body.append("ports=")
                        body.append(f"{ports}\n", style="cyan")
                except Exception as e:
                    body.append(f"  (taskdef 조회 실패: {type(e).__name__}: {e})\n", style="red")

                lbs = svc.get("loadBalancers", [])
                if lbs:
                    sect("Load Balancer:")
                    try:
                        elbv2 = self.session.client("elbv2", region_name=self.region)
                        for lb in lbs:
                            tg = lb.get("targetGroupArn")
                            port = lb.get("containerPort")
                            cname = lb.get("containerName", "")
                            if not tg:
                                body.append(f"  - classic LB {lb.get('loadBalancerName','?')}"
                                            f"  ({cname}:{port})\n")
                                continue
                            info = target_group_health(elbv2, tg)
                            counts = info["counts"]
                            body.append(f"  - {cname}:{port}  →  TG {short_arn(tg)}\n")
                            body.append("      targets: ", style="dim")
                            if counts:
                                parts = list(counts.items())
                                for j, (k, v) in enumerate(parts):
                                    body.append(f"{k}={v}", style=st_style(k))
                                    if j != len(parts) - 1:
                                        body.append(", ")
                                body.append("\n")
                            else:
                                body.append("대상 없음\n", style="yellow")
                            if info["lb"]:
                                lb_i = info["lb"]
                                body.append("      LB ", style="dim")
                                body.append(f"{lb_i['name']} (")
                                body.append(lb_i["state"], style=st_style(lb_i["state"]))
                                body.append(f")  {lb_i['dns']}\n", style="dim")
                    except Exception as e:
                        body.append(f"  (LB 조회 실패: {type(e).__name__}: {e})\n", style="red")
                else:
                    sect("Load Balancer:")
                    body.append("  연결 없음\n", style="dim")

                events = svc.get("events", [])[:5]
                if events:
                    sect("Recent events:")
                    for e in events:
                        ts = e.get("createdAt")
                        body.append("  - ")
                        if ts:
                            body.append(f"{ts:%Y-%m-%d %H:%M:%S}  ", style="dim")
                        body.append(f"{e.get('message','')}\n")
            else:
                body.append("(service 없음 - standalone task)\n", style="yellow")

            sect(f"Running tasks ({len(tasks)}):")
            for t in tasks:
                exec_on = t.get("enableExecuteCommand")
                body.append(f"  - {short_arn(t['taskArn'])}  ")
                body.append(f"{t.get('lastStatus')}", style=st_style(t.get("lastStatus")))
                body.append("  health=", style="dim")
                body.append(f"{t.get('healthStatus','-')}", style=st_style(t.get("healthStatus")))
                body.append("  exec=", style="dim")
                body.append("ON" if exec_on else "OFF", style="green" if exec_on else "red")
                body.append(f"  containers=[{','.join(c['name'] for c in t.get('containers', []))}]\n",
                            style="dim")

            self._output_is_logs = False
            title = f"상태  {cluster['clusterName']}/{service or '(no service)'}"
            self.call_from_thread(self.show_output, title, body)
            self.call_from_thread(self.set_status, "상태 표시됨 (오른쪽) · Esc 닫기")
        except Exception as e:
            self.call_from_thread(
                self._show_load_error, "상태 조회 오류", f"{type(e).__name__}: {e}")

    # ---- task definition 보기 (t) -------------------------------------
    @work(exclusive=True, thread=True)
    def show_taskdef(self):
        worker = get_current_worker()
        cluster = self.cluster
        service = self.service
        self.call_from_thread(self.set_status, "task definition 조회 중...")
        try:
            td_arn = None
            if service:
                svc = describe_service(self.ecs, cluster["clusterArn"], service)
                td_arn = svc.get("taskDefinition") if svc else None
            if not td_arn:
                tasks = list_running_tasks(self.ecs, cluster["clusterArn"], service)
                if tasks:
                    td_arn = tasks[0].get("taskDefinitionArn")
            if not td_arn:
                self.call_from_thread(self.show_error, "task definition 을 찾을 수 없습니다.")
                return
            td = get_task_definition(self.ecs, td_arn)
        except (BotoCoreError, ClientError) as e:
            self.call_from_thread(self.show_error, str(e))
            return
        if worker.is_cancelled:
            return

        body = json.dumps(td, indent=2, default=str, ensure_ascii=False)
        syntax = Syntax(body, "json", theme="ansi_dark", word_wrap=True)
        self._output_is_logs = False
        self.call_from_thread(self.show_output, f"task definition  {short_arn(td_arn)}", syntax)
        self.call_from_thread(self.set_status, "task definition 표시됨 (오른쪽) · Esc 닫기")

    # ---- 로그 보기 (l) : 최근 로그를 오른쪽 패널에 표시 ----------------
    @work(exclusive=True, thread=True)
    def show_logs(self, task, container_name):
        worker = get_current_worker()
        self.call_from_thread(self.set_status, "로그 조회 중...")
        try:
            td = get_task_definition(self.ecs, task["taskDefinitionArn"])
            cdef = next((c for c in td.get("containerDefinitions", [])
                         if c["name"] == container_name), None)
            cfg = awslogs_config(cdef) if cdef else None
            if not cfg or not cfg["group"]:
                self.call_from_thread(
                    self.show_error, f"'{container_name}' 는 awslogs 로그 드라이버가 아닙니다.")
                return

            logs = self.session.client("logs", region_name=cfg["region"] or self.region)
            kwargs = {
                "logGroupName": cfg["group"],
                "startTime": int((time.time() - 3600) * 1000),  # 최근 1시간
                "limit": 300,
            }
            if cfg["prefix"]:
                kwargs["logStreamNamePrefix"] = f"{cfg['prefix']}/{container_name}"
            resp = logs.filter_log_events(**kwargs)
            events = resp.get("events", [])
        except (BotoCoreError, ClientError) as e:
            self.call_from_thread(self.show_error, str(e))
            return
        if worker.is_cancelled:
            return

        events.sort(key=lambda e: e.get("timestamp", 0))
        events = events[-200:]
        lines = [self._fmt_log_event(e) for e in events]
        self._log_lines = lines
        self._log_title = f"로그  {cfg['group']}  [{container_name}]"
        self._log_search = ""
        self._log_cfg = cfg
        self._log_container = container_name
        self._log_last_ts = max((e["timestamp"] for e in events), default=0)
        self.call_from_thread(self._render_log)

    @staticmethod
    def _fmt_log_event(e):
        return (f"{time.strftime('%m-%d %H:%M:%S', time.localtime(e['timestamp'] / 1000))}  "
                f"{e.get('message', '').rstrip()}")

    # ---- tail -f (f) : 주기적으로 새 로그를 이어붙임 -------------------
    def action_follow(self):
        if not (self.output_active and self._output_is_logs):
            return
        if self._log_follow:
            self._stop_follow()
            self._render_log()
        else:
            self._log_follow = True
            self._log_follow_timer = self.set_interval(
                max(1, int(self.config.get("tail_interval_sec", 3))), self._poll_logs)
            self._render_log()

    def _stop_follow(self):
        self._log_follow = False
        if self._log_follow_timer is not None:
            self._log_follow_timer.stop()
            self._log_follow_timer = None

    def _poll_logs(self):
        if self._log_follow:
            self._poll_logs_worker()

    @work(thread=True)
    def _poll_logs_worker(self):
        cfg, container = self._log_cfg, self._log_container
        if not cfg:
            return
        try:
            logs = self.session.client("logs", region_name=cfg["region"] or self.region)
            kwargs = {
                "logGroupName": cfg["group"],
                "startTime": self._log_last_ts + 1,
                "limit": 300,
            }
            if cfg["prefix"]:
                kwargs["logStreamNamePrefix"] = f"{cfg['prefix']}/{container}"
            events = logs.filter_log_events(**kwargs).get("events", [])
        except (BotoCoreError, ClientError):
            return
        if not events or not self._log_follow:
            return
        events.sort(key=lambda e: e.get("timestamp", 0))
        self._log_last_ts = max(self._log_last_ts, events[-1]["timestamp"])
        new_lines = [self._fmt_log_event(e) for e in events]
        self._log_lines = (self._log_lines + new_lines)[-2000:]
        self.call_from_thread(self._render_log)

    def _render_log(self):
        """저장된 로그 줄을 검색어로 필터링해 오른쪽 패널에 표시(매칭 강조)."""
        follow_tag = " [green][tail -f][/green]" if self._log_follow else ""
        term = self._log_search.lower()
        if not self._log_lines:
            self.show_output(self._log_title, "(최근 1시간 로그 없음)")
            self.set_status("로그 없음 · f=tail · l 새로고침 · Esc 닫기")
            self._output_is_logs = True
            return
        shown = [ln for ln in self._log_lines if term in ln.lower()] if term else self._log_lines
        text = Text(no_wrap=False)
        if not shown:
            text.append("(검색 결과 없음)", style="dim")
        for ln in shown:
            lvl_style = log_level_style(ln) or ""
            if term:
                low = ln.lower()
                i = 0
                while True:
                    j = low.find(term, i)
                    if j < 0:
                        text.append(ln[i:], style=lvl_style)
                        break
                    text.append(ln[i:j], style=lvl_style)
                    text.append(ln[j:j + len(term)], style="black on yellow")
                    i = j + len(term)
                text.append("\n")
            else:
                text.append(ln + "\n", style=lvl_style)
        title = self._log_title + follow_tag + (
            f"   [검색: {_escape(self._log_search)}]" if term else "")
        self.show_output(title, text)
        self._output_is_logs = True
        # tail 모드면 맨 아래로 스크롤(새 로그 보이게)
        if self._log_follow:
            self.query_one("#output-scroll", VerticalScroll).scroll_end(animate=False)
        follow_hint = "f=tail중지" if self._log_follow else "f=tail"
        if term:
            self.set_status(f"검색 '{self._log_search}': {len(shown)}/{len(self._log_lines)}줄 · "
                            f"{follow_hint} · / 검색수정 · Esc 닫기")
        else:
            self.set_status(
                f"로그 {len(self._log_lines)}줄 · {follow_hint} · / 검색 · l 새로고침 · Esc 닫기")

    # ---- shell 접속 (s) : 오른쪽 패널에 임베드 터미널 ------------------
    def exec_into(self, task, container_name):
        cmd = [
            "aws", "ecs", "execute-command",
            "--profile", self.profile,
            "--region", self.region,
            "--cluster", self.cluster["clusterName"],
            "--task", short_arn(task["taskArn"]),
            "--container", container_name,
            "--interactive",
            "--command", self.command,
        ]
        title = f"shell  {self.cluster['clusterName']}/{container_name}"
        self._run_in_terminal(cmd, title)

    def _run_in_terminal(self, cmd, title):
        """창(우측 패널) 안의 임베드 터미널에 명령을 띄운다(화면 안 나감).

        오른쪽 결과 콘솔을 숨기고 임베드 터미널(pyte)로 세션을 표시한다.
        세션 종료(원격 exit/Ctrl+D 또는 F10) 시 _on_term_exit 로 복귀한다.
        """
        log.info("run_in_terminal(embed) cmd=%s", cmd)
        self._stop_follow()
        term = self.query_one("#terminal", TerminalPane)
        self.query_one("#output-scroll", VerticalScroll).add_class("hidden")
        term.add_class("visible")
        self.query_one("#output-title", Static).update(
            _escape(f"{title}   (F10 나가기 · PgUp/PgDn 스크롤)"))
        self.output_active = False
        term.start(cmd, on_exit=self._on_term_exit)

    def _on_term_exit(self):
        """임베드 터미널 세션 종료 → 결과 콘솔 복원, 목록으로 포커스."""
        term = self.query_one("#terminal", TerminalPane)
        dur = term.session_duration
        out = (term.last_output or "").strip()
        term.remove_class("visible")
        self.query_one("#output-scroll", VerticalScroll).remove_class("hidden")
        self.query_one("#output-title", Static).update("실행 결과")
        # 세션이 너무 빨리 끝났으면(대개 접속 실패) 마지막 출력을 결과창에 보여준다
        if dur < 3.0 and out:
            # #output 은 markup=False → 원문 그대로 전달(escape 시 백슬래시 노출)
            self.show_output("세션 종료 (조기 종료 - 출력 확인)", out)
        else:
            self.set_status("세션 종료")
        try:
            self.query_one(DataTable).focus()
        except Exception:
            pass


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="awscx",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        description="AWS ECS exec / EC2 SSM 접속용 k9s 스타일 터미널 TUI.",
        epilog=(
            "예시:\n"
            "  awscx                      프로파일 목록에서 선택\n"
            "  awscx -p my-profile        프로파일 지정\n"
            "  awscx -p my-profile -r us-east-1\n"
            "  python -m awscx -p my-profile\n"
            "\n"
            "실행 후 키:\n"
            "  Enter 상태 · s 접속 · l 로그 · f tail · t taskdef · p 프로파일\n"
            "  g 설정 · c 복사 · / 필터 · 번호 이동 · Ctrl+U/D 결과스크롤\n"
            "  F10 세션 나가기 · Esc 뒤로 · q 종료\n"
            "\n"
            "필요 도구: aws-cli v2, session-manager-plugin (별도 설치)\n"
            f"설정 파일: {CONFIG_PATH}"
        ),
    )
    parser.add_argument("--profile", "-p", default=None, metavar="NAME",
                        help="AWS profile 이름 (미지정 시 실행 후 목록에서 선택)")
    parser.add_argument("--region", "-r", default=None, metavar="REGION",
                        help="AWS region (미지정 시 config 값 사용)")
    parser.add_argument("--command", "-c", default=None, metavar="CMD",
                        help="shell 접속 시 실행할 command (미지정 시 config 값)")
    parser.add_argument("--version", "-v", action="version",
                        version=f"awscx {version_label()}")
    args = parser.parse_args(argv)

    if not shutil.which("aws"):
        sys.exit("[ERROR] aws-cli 를 찾을 수 없습니다. aws-cli v2 를 설치하세요.")
    if not shutil.which("session-manager-plugin"):
        sys.exit("[ERROR] session-manager-plugin 을 찾을 수 없습니다. 설치가 필요합니다.")

    cfg = load_config()
    if args.region:                 # CLI 인자가 config 보다 우선(이번 실행 한정)
        cfg["region"] = args.region
    if args.command:
        cfg["command"] = args.command
    setup_logging(cfg)
    print(f"[config] {CONFIG_PATH}")
    try:
        AwsExecApp(args.profile, cfg).run(mouse=bool(cfg.get("mouse", True)))
    except Exception:
        log.exception("앱 최상위 예외")
        raise
    finally:
        log.debug("===== awscx 종료 =====")

