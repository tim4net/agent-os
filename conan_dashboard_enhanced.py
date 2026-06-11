#!/usr/bin/env python3
"""
Conan Exiles Server Dashboard — Enhanced TUI for Steam Deck
Displays: live player map, player cards, kill feed, Clovis chat,
          FPS sparkline, events, system resources.
Interactive: send RCON commands, broadcast messages, talk to Clovis.
Data source: Clovis proxy at localhost:8090 + local system stats.
"""

import json
import os
import re
import subprocess
import threading
import time
from datetime import datetime

import urllib.request

from textual.app import App, ComposeResult
from textual.containers import Container, Vertical, Horizontal
from textual.reactive import reactive
from textual.widgets import (
    Header, Footer, Static, RichLog, Input,
)
from textual import work
from rich.text import Text

# ── Configuration ────────────────────────────────────────────────────────────
PROXY_URL = os.environ.get("CONAN_PROXY", "http://127.0.0.1:8090")
REFRESH_INTERVAL = 5          # /health + /events poll (seconds)
RCON_REFRESH_INTERVAL = 18    # RCON SQL poll (seconds) — don't hammer proxy

# Exiled Lands map bounds (UE coordinates in cm)
MAP_X_MIN, MAP_X_MAX = -350000, 250000
MAP_Y_MIN, MAP_Y_MAX = -400000, 300000
MAP_COLS = 56
MAP_ROWS = 16

# RCON serialization lock
_rcon_lock = threading.Lock()

# ── HTTP Helpers ─────────────────────────────────────────────────────────────

def fetch_json(path: str, timeout: float = 3.0) -> dict | None:
    try:
        with urllib.request.urlopen(f"{PROXY_URL}{path}", timeout=timeout) as resp:
            return json.loads(resp.read())
    except Exception:
        return None


def post_json(path: str, body: dict, timeout: float = 10.0) -> dict | None:
    try:
        data = json.dumps(body).encode("utf-8")
        req = urllib.request.Request(
            f"{PROXY_URL}{path}",
            data=data,
            headers={"Content-Type": "application/json"},
        )
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return json.loads(resp.read())
    except Exception as e:
        return {"error": str(e)}


def rcon_sql(query: str, timeout: float = 15.0) -> list[dict]:
    """Execute RCON SQL query; returns list of row dicts. Serialized."""
    with _rcon_lock:
        result = post_json("/rcon", {"command": f"sql {query}"}, timeout=timeout)
        if result and not result.get("error"):
            return parse_rcon_table(result.get("response", ""))
    return []


def rcon_command(command: str, timeout: float = 15.0) -> dict | None:
    """Execute raw RCON command. Serialized."""
    with _rcon_lock:
        return post_json("/rcon", {"command": command}, timeout=timeout)


# ── RCON Table Parser ───────────────────────────────────────────────────────

def parse_rcon_table(response: str) -> list[dict]:
    """Parse pipe-delimited RCON SQL output into list of dicts."""
    if not response:
        return []
    lines = response.strip().split("\n")
    if not lines:
        return []
    headers = [h.strip() for h in lines[0].split("|")]
    rows = []
    for line in lines[1:]:
        line = line.strip()
        if not line:
            continue
        if line.startswith("#"):
            parts = line.split(None, 1)
            line = parts[1] if len(parts) > 1 else ""
        values = [v.strip() for v in line.split("|")]
        if len(values) == len(headers):
            rows.append(dict(zip(headers, values)))
    return rows


# ── System Stats (from /proc) ───────────────────────────────────────────────

def get_system_stats() -> dict:
    stats = {}
    try:
        with open("/proc/uptime") as f:
            stats["uptime_s"] = float(f.read().split()[0])
    except Exception:
        stats["uptime_s"] = 0
    try:
        with open("/proc/meminfo") as f:
            lines = f.readlines()
        info = {}
        for line in lines:
            parts = line.split()
            info[parts[0].rstrip(":")] = int(parts[1])
        total = info.get("MemTotal", 1)
        avail = info.get("MemAvailable", 0)
        stats["mem_total_mb"] = total // 1024
        stats["mem_used_mb"] = (total - avail) // 1024
        stats["mem_pct"] = round((total - avail) / total * 100, 1)
    except Exception:
        stats["mem_pct"] = 0
    try:
        with open("/proc/stat") as f:
            line1 = f.readline()
        time.sleep(0.1)
        with open("/proc/stat") as f:
            line2 = f.readline()
        v1 = list(map(int, line1.split()[1:]))
        v2 = list(map(int, line2.split()[1:]))
        d_idle = v2[3] - v1[3]
        d_total = sum(v2) - sum(v1)
        stats["cpu_pct"] = round((1 - d_idle / d_total) * 100, 1) if d_total > 0 else 0
    except Exception:
        stats["cpu_pct"] = 0
    try:
        st = os.statvfs("/home/deck")
        total_gb = st.f_blocks * st.f_frsize / (1024**3)
        free_gb = st.f_bavail * st.f_frsize / (1024**3)
        stats["disk_total_gb"] = round(total_gb, 1)
        stats["disk_free_gb"] = round(free_gb, 1)
        stats["disk_pct"] = round((1 - free_gb / total_gb) * 100, 1) if total_gb > 0 else 0
    except Exception:
        stats["disk_pct"] = 0
    try:
        for zone in ["/sys/class/thermal/thermal_zone0/temp"]:
            if os.path.exists(zone):
                with open(zone) as f:
                    stats["cpu_temp_c"] = int(f.read().strip()) // 1000
                break
        else:
            stats["cpu_temp_c"] = 0
    except Exception:
        stats["cpu_temp_c"] = 0
    try:
        stats["load"] = os.getloadavg()
    except Exception:
        stats["load"] = (0, 0, 0)
    return stats


# ── Formatting Helpers ───────────────────────────────────────────────────────

def fmt_uptime(seconds: float) -> str:
    seconds = int(seconds)
    d, seconds = divmod(seconds, 86400)
    h, seconds = divmod(seconds, 3600)
    m, s = divmod(seconds, 60)
    if d > 0:
        return f"{d}d {h}h {m}m"
    if h > 0:
        return f"{h}h {m}m {s}s"
    return f"{m}m {s}s"


def fmt_ts(ts: str) -> str:
    try:
        dt = datetime.fromisoformat(ts.replace("Z", "+00:00"))
        return dt.astimezone().strftime("%H:%M:%S")
    except Exception:
        return ts[:19] if ts else ""


def clean_npc_name(name: str) -> str:
    if not name:
        return "Unknown"
    name = name.replace("_", " ").strip()
    name = re.sub(r"\s+\d+$", "", name)
    name = re.sub(r"\s+", " ", name).strip()
    return name or "Unknown"


def fmt_game_time(raw: str) -> str:
    """Parse game time response into display string."""
    if not raw:
        return ""
    raw = raw.strip()
    # Try HH:MM format
    m = re.match(r"(\d{1,2}):(\d{2})", raw)
    if m:
        h = int(m.group(1))
        icon = "\u2600" if 6 <= h < 18 else "\u263D"  # ☀ or ☽
        return f"{icon} {raw}"
    # Try decimal hour
    try:
        h = float(raw)
        hh = int(h)
        mm = int((h - hh) * 60)
        icon = "\u2600" if 6 <= hh < 18 else "\u263D"
        return f"{icon} {hh:02d}:{mm:02d}"
    except ValueError:
        pass
    return raw[:20]


# ── ASCII Map Builder ────────────────────────────────────────────────────────

# Biome label positions (row_frac, col_frac, label, style)
_BIOME_LABELS = [
    (0.06, 0.45, "Snow", "dim white"),
    (0.20, 0.72, "Volcano", "dim red"),
    (0.40, 0.50, "Highlands", "dim"),
    (0.55, 0.12, "Jungle", "dim green"),
    (0.78, 0.65, "Desert", "dim yellow"),
    (0.85, 0.30, "Swamp", "dim cyan"),
]


def build_map_text(positions: list[dict]) -> Text:
    """Build an ASCII map of the Exiled Lands with player markers."""
    text = Text()
    text.append("  EXILED LANDS MAP", style="bold")
    online_n = sum(1 for p in positions if int(p.get("online", 0)))
    text.append(f"  {online_n} online, {len(positions)} tracked\n", style="dim")

    # Calculate grid positions for each player
    placements = []
    for p in positions:
        try:
            x = float(p.get("x", 0))
            y = float(p.get("y", 0))
        except (ValueError, TypeError):
            continue
        if abs(x) < 5000 and abs(y) < 5000:
            continue  # unspawned
        col = int((x - MAP_X_MIN) / (MAP_X_MAX - MAP_X_MIN) * (MAP_COLS - 1))
        row = int((MAP_Y_MAX - y) / (MAP_Y_MAX - MAP_Y_MIN) * (MAP_ROWS - 1))
        col = max(1, min(MAP_COLS - 8, col))
        row = max(0, min(MAP_ROWS - 1, row))
        name = p.get("char_name", "?")[:7]
        online = bool(int(p.get("online", 0)))
        placements.append((row, col, name, online))

    # Resolve overlaps by shifting right or down
    occupied_cells = set()
    resolved = []
    for row, col, name, online in sorted(placements, key=lambda p: (p[0], p[1])):
        placed = False
        for row_off in range(MAP_ROWS):
            for col_off in range(0, MAP_COLS - col - len(name) - 1, 2):
                tr = min(MAP_ROWS - 1, row + row_off)
                tc = col + col_off
                cells = set(range(tc, tc + len(name) + 1))
                if not cells & occupied_cells:
                    resolved.append((tr, tc, name, online))
                    occupied_cells |= cells
                    placed = True
                    break
            if placed:
                break
        if not placed:
            resolved.append((row, col, name, online))

    # Build per-row render data: markers at (row, col) -> (name, online)
    markers = {}
    for row, col, name, online in resolved:
        markers[(row, col)] = (name, online)

    # Top border
    text.append("  \u250C")
    text.append("\u2500" * MAP_COLS)
    text.append("\u2510\n")

    # Render rows
    for r in range(MAP_ROWS):
        text.append("  \u2502")

        # Check for biome label on this row
        biome_on_row = None
        for rf, cf, label, style in _BIOME_LABELS:
            br = int(rf * (MAP_ROWS - 1))
            if br == r:
                biome_on_row = (int(cf * (MAP_COLS - 1)), label, style)
                break

        c = 0
        while c < MAP_COLS:
            if (r, c) in markers:
                name, online = markers[(r, c)]
                style = "bold bright_green" if online else "dim yellow"
                marker = "@" if online else "+"
                text.append(marker + name, style=style)
                c += 1 + len(name)
            elif biome_on_row and c == biome_on_row[0]:
                label, style = biome_on_row[1], biome_on_row[2]
                text.append(label, style=style)
                c += len(label)
            else:
                text.append(" ")
                c += 1

        text.append("\u2502\n")

    # Bottom border
    text.append("  \u2514")
    text.append("\u2500" * MAP_COLS)
    text.append("\u2518")

    # Legend
    text.append("\n  ", style="dim")
    text.append("@", style="bold bright_green")
    text.append("=online  ", style="dim")
    text.append("+", style="dim yellow")
    text.append("=offline", style="dim")

    return text


# ── Player Cards Builder ────────────────────────────────────────────────────

def build_player_cards_text(players: list[dict]) -> Text:
    text = Text()
    text.append("  PLAYER CARDS", style="bold")
    if not players:
        text.append("\n  No character data", style="dim")
        return text

    for p in players:
        name = p.get("char_name", "???")
        level = p.get("level", "?")
        online = bool(int(p.get("online", 0)))
        alive = bool(int(p.get("isAlive", 1)))
        last_time = p.get("lastServerTimeOnline", "0")

        text.append("\n  ")
        if online:
            text.append("@", style="bold bright_green")
            text.append(f" {name}", style="bold bright_green")
        else:
            text.append("+", style="dim yellow")
            text.append(f" {name}", style="yellow")
        text.append(f" Lv{level}", style="bold")
        if alive:
            text.append(" ALIVE", style="green")
        else:
            text.append(" DEAD", style="bold red")
        if online:
            text.append(" [ONLINE]", style="bold green")
        else:
            text.append(" [OFFLINE]", style="dim")

    return text


# ── Kill Feed Builder ────────────────────────────────────────────────────────

def build_kill_feed_entry(kill: dict) -> Text:
    """Format a single kill event for the kill feed."""
    causer = kill.get("causerName", "")
    obj = kill.get("objectName", "")
    etype = kill.get("eventType", "")

    text = Text("  ")

    if etype in ("114", "115") and causer and obj:
        # Player killed NPC
        text.append(f"{causer}", style="bold red")
        text.append(" killed ", style="dim")
        text.append(clean_npc_name(obj), style="yellow")
    elif etype == "103" and causer:
        # NPC killed player
        text.append(f"{causer}", style="bold red")
        text.append(" died to ", style="dim")
        text.append(clean_npc_name(obj) if obj else "environment", style="yellow")
    elif etype == "103":
        text.append("Environmental death", style="dim red")
    else:
        text.append(f"{causer or '?'} vs {clean_npc_name(obj)}", style="dim")

    return text


# ── FPS Builder ──────────────────────────────────────────────────────────────

def build_fps_text(history: list) -> Text:
    if not history:
        return Text("  FPS: --")
    current = history[-1]
    avg = sum(history) / len(history)
    min_fps = min(history)
    max_fps = max(history)
    if current >= 25:
        style = "bold green"
    elif current >= 15:
        style = "bold yellow"
    else:
        style = "bold red"
    spark_chars = "._-~=*#"
    spark = ""
    if max_fps > min_fps:
        for v in history[-40:]:
            idx = int((v - min_fps) / (max_fps - min_fps) * (len(spark_chars) - 1))
            spark += spark_chars[max(0, min(idx, len(spark_chars) - 1))]
    else:
        spark = "=" * min(len(history), 40)
    line = Text()
    line.append("  FPS: ")
    line.append(f"{current:.1f}", style=style)
    line.append(f"  avg:{avg:.1f}  min:{min_fps:.1f}  max:{max_fps:.1f}")
    line.append(f"\n  {spark}", style="dim cyan")
    return line


# ── System Resources Builder ────────────────────────────────────────────────

def build_system_text(stats: dict) -> Text:
    cpu = stats.get("cpu_pct", 0)
    mem = stats.get("mem_pct", 0)
    mem_used = stats.get("mem_used_mb", 0)
    mem_total = stats.get("mem_total_mb", 0)
    disk = stats.get("disk_pct", 0)
    disk_free = stats.get("disk_free_gb", 0)
    disk_total = stats.get("disk_total_gb", 0)
    temp = stats.get("cpu_temp_c", 0)
    load = stats.get("load", (0, 0, 0))

    def bar(pct, width=20):
        filled = int(pct / 100 * width)
        return "#" * filled + "-" * (width - filled)

    def color_for(pct):
        if pct < 60:
            return "green"
        if pct < 85:
            return "yellow"
        return "red"

    lines = Text()
    lines.append("  SYSTEM RESOURCES\n", style="bold")
    lines.append("  CPU  ")
    lines.append(f"{bar(cpu)}", style=color_for(cpu))
    lines.append(f" {cpu:.0f}%  ({temp}C)\n")
    lines.append("  RAM  ")
    lines.append(f"{bar(mem)}", style=color_for(mem))
    lines.append(f" {mem:.0f}%  ({mem_used}/{mem_total} MB)\n")
    lines.append("  Disk ")
    lines.append(f"{bar(disk)}", style=color_for(disk))
    lines.append(f" {disk:.0f}%  ({disk_free:.0f}/{disk_total:.0f} GB free)\n")
    lines.append("  Load ", style="dim")
    lines.append(f"{load[0]:.1f}  {load[1]:.1f}  {load[2]:.1f}", style="dim")
    return lines


# ── Event Formatter ─────────────────────────────────────────────────────────

def fmt_event(event: dict, show_stats: bool = False) -> Text | None:
    etype = event.get("type", "unknown")
    ts = fmt_ts(event.get("timestamp", ""))
    data = event.get("data", {})

    if etype == "server_stats":
        if not show_stats:
            return None
        fps = data.get("fps", "?")
        players = data.get("players", "?")
        uptime_s = data.get("uptime", 0)
        return Text(f"  {ts}  FPS:{fps}  Players:{players}  Up:{fmt_uptime(uptime_s)}", style="dim")

    elif etype == "player_join":
        name = data.get("player_name", data.get("name", data.get("player", "Unknown")))
        return Text(f"  {ts}  ").append(f">> {name} joined", style="bold green")

    elif etype == "player_disconnect":
        name = data.get("player_name", data.get("name", data.get("player", "Unknown")))
        sid = data.get("steam_id", "")
        label = name if name != "Unknown" else f"Steam:{sid}"
        return Text(f"  {ts}  ").append(f"<< {label} left", style="bold red")

    elif etype == "player_death":
        victim = data.get("victim", "Unknown")
        killer = data.get("killer", "")
        zone = data.get("zone", "")
        if killer and killer not in ("CauseOfDeath:", "None.", "", victim):
            msg = f"X {victim} killed by {killer}"
        else:
            msg = f"X {victim} died"
        if zone and zone != "unknown":
            msg += f" [{zone}]"
        return Text(f"  {ts}  ").append(msg, style="bold yellow")

    elif etype in ("chat", "player_chat"):
        name = data.get("player_name", data.get("name", data.get("player", "Unknown")))
        msg = data.get("message", "")
        return Text(f"  {ts}  ").append(f"{name}: ", style="bold cyan").append(msg)

    elif etype in ("clovis_say", "clovis_response", "clovis_greeting", "broadcast"):
        msg = data.get("message", "")
        return Text(f"  {ts}  ").append("Clovis: ", style="bold magenta").append(msg, style="magenta")

    else:
        return Text(f"  {ts}  [{etype}] {json.dumps(data)[:80]}", style="dim")


# ── Main Dashboard App ──────────────────────────────────────────────────────

class ConanDashboard(App):
    TITLE = "Conan Exiles Dashboard"
    SUB_TITLE = "Steam Deck Monitor"

    CSS = """
    Screen {
        layout: vertical;
        background: $surface;
    }
    #top-bar {
        height: 3;
        border-bottom: solid $primary;
        padding: 1;
    }
    #main-content {
        layout: horizontal;
        height: 1fr;
    }
    #left-panel {
        width: 62%;
        border-right: solid $primary;
        layout: vertical;
    }
    #right-panel {
        width: 38%;
        layout: vertical;
    }
    #map-section {
        height: auto;
        max-height: 22;
        border-bottom: solid $primary;
        padding: 0 1;
    }
    #event-header {
        background: $surface-darken-1;
        padding: 0 1;
        height: 1;
    }
    #event-log {
        height: 1fr;
        border: none;
        padding: 0 1;
        scrollbar-size: 1 1;
    }
    #player-section {
        height: auto;
        max-height: 10;
        border-bottom: solid $primary;
        padding: 0 1;
    }
    #kill-feed-header {
        background: $surface-darken-1;
        padding: 0 1;
        height: 1;
    }
    #kill-feed {
        height: auto;
        max-height: 8;
        border-bottom: solid $primary;
        padding: 0 1;
        scrollbar-size: 1 1;
    }
    #clovis-header {
        background: $surface-darken-1;
        padding: 0 1;
        height: 1;
    }
    #clovis-chat {
        height: auto;
        max-height: 6;
        border-bottom: solid $primary;
        padding: 0 1;
        scrollbar-size: 1 1;
    }
    #fps-section {
        height: 5;
        border-bottom: solid $primary;
        padding: 0 1;
    }
    #system-section {
        height: auto;
        padding: 0 1;
    }
    #command-bar {
        dock: bottom;
        height: 3;
        border-top: solid $primary;
        background: $surface-darken-1;
        padding: 0 1;
    }
    #command-input {
        width: 100%;
        height: 1;
        margin: 1 0;
    }
    """

    BINDINGS = [
        ("q", "quit", "Quit"),
        ("r", "force_refresh", "Refresh"),
        ("/", "focus_command", "Command"),
        ("escape", "unfocus_command", "Back"),
    ]

    fps_history: reactive[list] = reactive(list)
    last_event_id: reactive[int] = reactive(0)

    # Cached RCON data
    _cached_players: list[dict] = []
    _cached_positions: list[dict] = []
    _cached_kills: list[dict] = []
    _cached_game_time: str = ""
    _rcon_stale: bool = True

    def compose(self) -> ComposeResult:
        yield Header(show_clock=True)
        yield Static(id="top-bar")
        with Container(id="main-content"):
            with Vertical(id="left-panel"):
                yield Static(id="map-section")
                yield Static(Text("  EVENTS", style="bold"), id="event-header")
                yield RichLog(id="event-log", highlight=True, markup=False)
            with Vertical(id="right-panel"):
                yield Static(id="player-section")
                yield Static(Text("  KILL FEED", style="bold red"), id="kill-feed-header")
                yield RichLog(id="kill-feed", highlight=True, markup=False)
                yield Static(Text("  CLOVIS", style="bold magenta"), id="clovis-header")
                yield RichLog(id="clovis-chat", highlight=True, markup=False)
                yield Static(id="fps-section")
                yield Static(id="system-section")
        with Container(id="command-bar"):
            yield Input(
                placeholder="/broadcast /rcon /clovis /say /save /day /night /time",
                id="command-input",
            )
        yield Footer()

    def on_mount(self) -> None:
        # Initialize all panels
        self.query_one("#top-bar", Static).update(Text("  Loading...", style="dim"))
        self.query_one("#map-section", Static).update(
            Text("  MAP: Waiting for RCON data...", style="dim")
        )
        self.query_one("#player-section", Static).update(
            Text("  PLAYERS\n  Loading...", style="dim")
        )
        self.query_one("#fps-section", Static).update(Text("  FPS: --"))
        self.query_one("#system-section", Static).update(
            Text("  Loading...", style="dim")
        )

        # Event log help
        el = self.query_one("#event-log", RichLog)
        el.write(Text("  Conan Server Dashboard", style="bold"))
        el.write(Text("  Press / to open command bar, Esc to go back", style="dim"))
        el.write(
            Text(
                "  Commands: /broadcast /rcon /say /clovis /save /day /night /time",
                style="dim",
            )
        )
        el.write(Text("  " + "\u2500" * 50, style="dim"))

        # Clovis chat welcome
        cl = self.query_one("#clovis-chat", RichLog)
        cl.write(Text("  Clovis is listening...", style="dim magenta"))

        # Start refresh intervals
        self.refresh_data()
        self.set_interval(REFRESH_INTERVAL, self.refresh_data)

        self.refresh_rcon_data()
        self.set_interval(RCON_REFRESH_INTERVAL, self.refresh_rcon_data)

    # ── Actions ──────────────────────────────────────────────────────────

    def action_focus_command(self):
        self.query_one("#command-input", Input).focus()

    def action_unfocus_command(self):
        inp = self.query_one("#command-input", Input)
        inp.can_focus = False
        inp.can_focus = True
        self.set_focus(None)

    def action_force_refresh(self):
        self.refresh_data()
        self.refresh_rcon_data()

    # ── Command Input ────────────────────────────────────────────────────

    def on_input_submitted(self, event: Input.Submitted):
        cmd = event.value.strip()
        if not cmd:
            return
        input_widget = self.query_one("#command-input", Input)
        input_widget.value = ""
        event_log = self.query_one("#event-log", RichLog)
        event_log.write(Text(f"  > {cmd}", style="bold white"))

        if cmd.startswith("/broadcast "):
            self._do_broadcast(cmd[len("/broadcast "):])
        elif cmd.startswith("/rcon "):
            self._do_rcon(cmd[len("/rcon "):])
        elif cmd.startswith("/clovis "):
            self._do_clovis(cmd[len("/clovis "):])
        elif cmd.startswith("/say "):
            self._do_broadcast(f"[Dashboard] {cmd[len('/say '):]}")
        elif cmd == "/save":
            self._do_rcon("saveworld")
        elif cmd == "/day":
            self._do_rcon("settime 12")
        elif cmd == "/night":
            self._do_rcon("settime 0")
        elif cmd == "/time":
            self._do_rcon("gettime")
        elif cmd == "/players":
            self._do_rcon("listplayers", show_names=True)
        else:
            self._do_broadcast(cmd)

    @work(thread=True)
    def _do_rcon(self, command: str, show_names: bool = False):
        event_log = self.query_one("#event-log", RichLog)
        result = rcon_command(command)
        if result and not result.get("error"):
            resp = result.get("response", "no response")
            self.call_from_thread(
                event_log.write,
                Text(f"  RCON [{command}]: ", style="bold yellow").append(
                    resp, style="white"
                ),
            )
            if show_names and resp:
                names = []
                for line in resp.strip().split("\n"):
                    m = re.match(r"\d+\s*\|\s*(.+?)\s*\|\s*\d+", line)
                    if m:
                        names.append({"name": m.group(1).strip()})
                if names:
                    self.call_from_thread(
                        self.query_one("#player-section", Static).update,
                        build_player_cards_text_from_names(names),
                    )
        else:
            self.call_from_thread(
                event_log.write,
                Text(f"  RCON [{command}]: ", style="bold yellow").append(
                    result.get("error", "failed") if result else "failed",
                    style="bold red",
                ),
            )

    @work(thread=True)
    def _do_broadcast(self, message: str):
        event_log = self.query_one("#event-log", RichLog)
        result = post_json("/broadcast", {"message": message}, timeout=10)
        self.call_from_thread(
            event_log.write,
            Text("  BROADCAST: ", style="bold green")
            .append(message)
            .append(
                f"  -> {result.get('response', 'ok') if result else 'failed'}",
                style="dim",
            ),
        )

    @work(thread=True)
    def _do_clovis(self, message: str):
        """Send a message to Clovis AI chat."""
        event_log = self.query_one("#event-log", RichLog)
        clovis_log = self.query_one("#clovis-chat", RichLog)

        # Log the outgoing message
        self.call_from_thread(
            clovis_log.write,
            Text(f"  You: ", style="bold cyan").append(message),
        )

        # Try /clovis/chat endpoint, fallback to broadcast
        result = post_json("/clovis/chat", {"message": message}, timeout=15)
        if result and not result.get("error"):
            resp = result.get("response", "")
            if resp:
                self.call_from_thread(
                    clovis_log.write,
                    Text("  Clovis: ", style="bold magenta").append(resp, style="magenta"),
                )
        else:
            # Fallback: broadcast so Clovis can see it in-game
            self._do_broadcast(f"[To Clovis] {message}")
            self.call_from_thread(
                event_log.write,
                Text(
                    f"  Clovis chat endpoint unavailable, broadcast sent",
                    style="dim yellow",
                ),
            )

    # ── Regular Data Refresh (/health + /events) ────────────────────────

    @work(thread=True)
    def refresh_data(self) -> None:
        health_data = fetch_json("/health", timeout=3)
        events_data = fetch_json("/events", timeout=5)
        sys_stats = get_system_stats()
        self.call_from_thread(self._update_ui, health_data, events_data, sys_stats)

    def _update_ui(self, health_data, events_data, sys_stats):
        # ── Top bar ──
        if health_data:
            line = Text()
            line.append("  * ONLINE", style="bold green")
            line.append(f"  |  Up: {fmt_uptime(sys_stats.get('uptime_s', 0))}")
            line.append(f"  |  Proxy: {fmt_uptime(health_data.get('uptime', 0))}")
            if self._cached_game_time:
                line.append(f"  |  Game: {self._cached_game_time}")
            line.append(f"  |  {datetime.now().strftime('%H:%M:%S')}")
            self.query_one("#top-bar", Static).update(line)
        else:
            self.query_one("#top-bar", Static).update(
                Text("  !! PROXY OFFLINE", style="bold red")
            )

        # ── System ──
        self.query_one("#system-section", Static).update(build_system_text(sys_stats))

        if events_data is None:
            return

        events = events_data.get("events", [])

        # ── FPS ──
        for e in reversed(events):
            if e.get("type") == "server_stats":
                try:
                    fps = float(e["data"].get("fps", 0))
                    history = self.fps_history[-60:] + [fps]
                    self.fps_history = history
                except (ValueError, TypeError):
                    pass
                break
        self.query_one("#fps-section", Static).update(build_fps_text(self.fps_history))

        # ── Events ──
        event_log = self.query_one("#event-log", RichLog)
        new_events = [e for e in events if e.get("id", 0) > self.last_event_id]
        if new_events:
            new_events.sort(key=lambda e: e.get("id", 0))
            for e in new_events:
                formatted = fmt_event(e, show_stats=False)
                if formatted:
                    event_log.write(formatted)

                # Route Clovis events to Clovis panel
                etype = e.get("type", "")
                if etype in ("clovis_say", "clovis_response", "clovis_greeting"):
                    clovis_log = self.query_one("#clovis-chat", RichLog)
                    msg = e.get("data", {}).get("message", "")
                    ts = fmt_ts(e.get("timestamp", ""))
                    if msg:
                        clovis_log.write(
                            Text(f"  {ts}  ").append(
                                f"Clovis: {msg}", style="magenta"
                            )
                        )

                # Sound on player join
                if e.get("type") == "player_join":
                    try:
                        subprocess.run(
                            [
                                "paplay",
                                "--volume=32768",
                                "/usr/share/sounds/freedesktop/stereo/complete.oga",
                            ],
                            timeout=2,
                            capture_output=True,
                        )
                    except Exception:
                        pass

                self.last_event_id = max(self.last_event_id, e.get("id", 0))

    # ── RCON Data Refresh (players, positions, kills, time) ─────────────

    @work(thread=True)
    def refresh_rcon_data(self) -> None:
        """Fetch all RCON data in a single serialized pass."""
        players = rcon_sql(
            "SELECT c.char_name, c.level, c.isAlive, "
            "c.lastServerTimeOnline, a.online, c.id "
            "FROM characters c JOIN account a ON c.playerId = a.id"
        )

        positions = rcon_sql(
            "SELECT ap.x, ap.y, ap.z, c.char_name, a.online, c.id "
            "FROM actor_position ap JOIN characters c ON ap.id = c.id "
            "JOIN account a ON c.playerId = a.id "
            "WHERE ap.class LIKE '%BasePlayerChar%'"
        )

        kills = rcon_sql(
            "SELECT ge.serverTime, ge.causerName, ge.objectName, "
            "ge.x, ge.y, ge.z, ge.eventType "
            "FROM game_events ge "
            "WHERE ge.eventType IN (103, 114, 115) "
            "ORDER BY ge.serverTime DESC LIMIT 20"
        )

        # In-game time
        game_time_raw = ""
        time_result = rcon_command("gettime")
        if time_result and not time_result.get("error"):
            game_time_raw = time_result.get("response", "")

        self.call_from_thread(
            self._update_rcon_ui, players, positions, kills, game_time_raw
        )

    def _update_rcon_ui(self, players, positions, kills, game_time_raw):
        # Cache data
        self._cached_players = players
        self._cached_positions = positions
        self._cached_kills = kills
        self._rcon_stale = False

        # Game time
        if game_time_raw:
            self._cached_game_time = fmt_game_time(game_time_raw)

        # ── Player Map ──
        if positions:
            self.query_one("#map-section", Static).update(build_map_text(positions))
        else:
            self.query_one("#map-section", Static).update(
                Text("  MAP: No position data yet", style="dim")
            )

        # ── Player Cards ──
        if players:
            self.query_one("#player-section", Static).update(
                build_player_cards_text(players)
            )
        else:
            self.query_one("#player-section", Static).update(
                Text("  PLAYERS\n  No data", style="dim")
            )

        # ── Kill Feed ──
        kill_feed = self.query_one("#kill-feed", RichLog)
        if kills:
            kill_feed.clear()
            for k in kills[:12]:
                kill_feed.write(build_kill_feed_entry(k))


# ── Helper for simple player name list ──────────────────────────────────────

def build_player_cards_text_from_names(names: list[dict]) -> Text:
    text = Text()
    text.append(f"  PLAYERS ({len(names)})\n", style="bold")
    for p in names:
        name = p.get("name", str(p))
        text.append(f"  * {name}\n", style="bold cyan")
    return text


# ── Entry Point ──────────────────────────────────────────────────────────────

if __name__ == "__main__":
    app = ConanDashboard()
    app.run()
