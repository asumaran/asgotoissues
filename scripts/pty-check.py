#!/usr/bin/env python3
"""End-to-end TUI check for asgotoissues without a real terminal.

Spawns the binary on a pty, answers the terminal queries bubbletea sends
(OSC 10/11, CSI 6n, DA1), replays keystrokes, and asserts on frames rendered
with pyte. Everything runs in a throwaway sandbox: a fake HOME, a synthetic
config (ASGOTOISSUES_CONFIG), an empty netrc, a fresh synthetic ticket cache (so
nothing is fetched) and a logging stub instead of the browser
(ASGOTOISSUES_OPEN_CMD). It never reads the real config and never talks to Jira
or GitHub (gh does not have to be installed).

Usage: scripts/pty-check.py ./asgotoissues   (needs python3 + pyte)
"""
NAME, ROWS, COLS = "asgotoissues", 18, 150
import atexit, fcntl, json, os, pty, select, shutil, signal, struct, subprocess, sys, tempfile, termios, time, re
import pyte

BIN = os.path.abspath(sys.argv[1])
SANDBOX = os.path.realpath(tempfile.mkdtemp(prefix="%s-pty-" % NAME))
home = os.path.join(SANDBOX, "home")
os.makedirs(home)

def write(path, text, mode=None):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        f.write(text)
    if mode: os.chmod(path, mode)
    return path

QUERIES = [(b"\x1b]11;?", b"\x1b]11;rgb:0000/0000/0000\x1b\\"), (b"\x1b]10;?", b"\x1b]10;rgb:ffff/ffff/ffff\x1b\\"),
           (b"\x1b[6n", b"\x1b[1;1R"), (b"\x1b[c", b"\x1b[?62c")]

failures = []
def check(cond, msg):
    print(("  ok   " if cond else "  FAIL ") + msg)
    if not cond: failures.append(msg)

class Session:
    """One run of the binary on a pty."""
    def __init__(self, env, args=(), cwd=None):
        self.master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))
        self.proc = subprocess.Popen([BIN, *args], stdin=slave, stdout=slave, stderr=slave, env=env,
                                     close_fds=True, cwd=cwd or SANDBOX)
        os.close(slave)
        # A failed assertion must not leave the binary running on a dead pty.
        atexit.register(lambda p=self.proc: p.poll() is None and p.kill())
        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.ByteStream(self.screen)
        self.raw = bytearray()
        self.answered = 0

    def pump(self, seconds):
        end = time.time() + seconds
        while True:
            left = end - time.time()
            if left <= 0: break
            r, _, _ = select.select([self.master], [], [], left)
            if not r: continue
            try:
                data = os.read(self.master, 65536)
            except OSError:
                break
            if not data: break
            self.raw.extend(data); self.stream.feed(data)
            tail = bytes(self.raw[self.answered:])
            for q, reply in QUERIES:
                for _ in range(tail.count(q)):
                    os.write(self.master, reply)
            self.answered = len(self.raw)

    def repaint(self):
        # The v2 renderer updates the screen with scroll regions and SU, which
        # pyte ignores; a resize forces a full redraw it can follow.
        for cols in (COLS - 1, COLS):
            fcntl.ioctl(self.master, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, cols, 0, 0))
            self.screen.resize(ROWS, cols)
            if self.proc.poll() is None: os.kill(self.proc.pid, signal.SIGWINCH)
            self.pump(0.3)

    def frame(self):
        return [line.rstrip() for line in self.screen.display]

    def send(self, b, wait=0.4):
        os.write(self.master, b); self.pump(wait); self.repaint()
        return self.frame()

    def start(self, marker):
        for _ in range(50):
            self.pump(0.1)
            if marker in "\n".join(self.frame()): break
        self.pump(0.5); self.repaint()
        return self.frame()

    def finish(self):
        try:
            self.proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            return None
        self.pump(0.2)
        return self.proc.returncode

def dump(title, f):
    print("--- %s ---" % title)
    for i, l in enumerate(f): print("%2d|%s" % (i, l))

def done():
    shutil.rmtree(SANDBOX, ignore_errors=True)
    print("\n%d failure(s)" % len(failures))
    sys.exit(1 if failures else 0)

CTRL_A, CTRL_S, CTRL_T, ESC, ENTER, TAB, DOWN, UP = b"\x01", b"\x13", b"\x14", b"\x1b", b"\r", b"\t", b"\x1b[B", b"\x1b[A"

# ---------- sandbox: config, netrc, fresh cache, browser stub ----------
config = write(os.path.join(home, "asdev.local.md"), """---
stacks:
  acme:
    jira:
      type: cloud
      base_url: https://acme.example
      email: me@acme.example
      api_token_env: JIRA_TOKEN_ACME
  globex:
    jira:
      type: cloud
      base_url: https://globex.example
      email: me@globex.example
      api_token_env: JIRA_TOKEN_GLOBEX
  home:
    issues: [github]
    github:
      org: me
---
""")
netrc = write(os.path.join(home, ".netrc"), "")
now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
def ticket(key, stack, summary, status, cat, desc="", parent=""):
    t = {"key": key, "stack": stack, "url": "https://%s.example/browse/%s" % (stack, key), "summary": summary,
         "description": desc, "status": status, "status_cat": cat, "type": "Task", "priority": "Medium",
         "project": key.split("-")[0], "created": now, "updated": now}
    if parent: t["parent_key"] = parent
    return t
tickets = [
    ticket("PLAT-2099", "acme", "Enforce authz on the export endpoint", "In Progress", "In Progress",
           "h2. Context\n\nThe *export* endpoint skips the policy check.\n\n* add the guard\n* cover it with a test"),
    ticket("PLAT-2098", "acme", "Audit trail at the service boundary", "To Do", "To Do"),
    ticket("SHOP-602", "globex", "Create test fixtures", "Open", "To Do", parent="SHOP-600"),
    # a GitHub issue: its own source, a Markdown body, the meta line comes with it
    {"key": "tool#12", "stack": "home", "source": "github", "url": "https://github.com/me/tool/issues/12",
     "summary": "Cache layer rewrite", "description": "Rename `snake_case_name`.\n\n* keep the bullet",
     "body_format": "markdown", "status": "open", "state": "doing", "status_cat": "", "type": "", "priority": "",
     "project": "me/tool", "meta": ["me/tool", "bug, in progress"], "created": now, "updated": now},
]
write(os.path.join(home, ".config", "herdr", "asgotoissues-tui", "issuecache.json"),
      json.dumps({"fetched_at": now, "issues": tickets}))
open_log = os.path.join(SANDBOX, "open.log")
opener = write(os.path.join(SANDBOX, "opener"), '#!/bin/sh\nprintf "%%s\\n" "$1" >> "%s"\n' % open_log, 0o755)

def session():
    env = dict(os.environ, TERM="xterm-256color", COLORTERM="truecolor", HOME=home, NETRC=netrc,
               ASGOTOISSUES_CONFIG=config, ASGOTOISSUES_OPEN_CMD=opener, XDG_CONFIG_HOME=os.path.join(home, ".config"))
    for k in ("HERDR_PLUGIN_STATE_DIR", "JIRA_TOKEN_ACME", "JIRA_TOKEN_GLOBEX"):
        env.pop(k, None)
    if os.path.exists(open_log): os.remove(open_log)
    return Session(env)

def opened():
    time.sleep(0.3)
    return open(open_log).read().splitlines() if os.path.exists(open_log) else []

# One frame (see frame.go): top border with the counter, input, main edge,
# list | preview, bottom edge, help, border. There is no context line.
def listw(): return max(COLS - 3 - (COLS - 2) * 75 // 100, 10)   # the default split: list 25%, preview 75%
def divider(f): return next(l for l in f if l.startswith("├") and "┬" in l).index("┬")
SHIFT_RIGHT, SHIFT_LEFT = b"\x1b[1;2C", b"\x1b[1;2D"
def left(f):  return [l[1:1 + listw()].rstrip() for l in f[3:-3] if l[1:1 + listw()].strip()]
# The input line: the prompt and what is typed (or the placeholder). A build
# that is not a release says "(dev)" at the end of the edge over the
# input; devmark() says so.
def prompt(f): return f[1].strip("│ ").rstrip().removesuffix("(dev)").rstrip()
def devmark(f): return any(l.rstrip("╮┤─ ").endswith("(dev)") for l in f[:4])
def counter(f):
    for l in f:
        m = re.match(r"├─+ (\d+/\d+) ─[┴┤]", l)
        if m: return m.group(1)
    return ""
def status(f): return f[0].strip("╭╮─ ").removesuffix("(dev)").rstrip()

print("== asgotoissues pty driver (%dx%d) ==" % (COLS, ROWS))

# ---------- run 1: grouped list, preview, filter by number, open ----------
s = session()
f = s.start("asgotoissues ❯"); dump("open", f)
check(prompt(f) == "asgotoissues ❯ Search by title, key, status, repo…", "prompt line is clean: %r" % f[1])
check(devmark(f), "a dev build says so on the edge over the input")
check(f[0].startswith("╭") and f[-1].startswith("╰") and "┬" in f[2],
      "one frame: input right under the top border, no title line")
check(counter(f) == "4/4" and "type filter" in f[-2] and "esc/q quit" in f[-2], "counter %r and help %r" % (counter(f), f[-2]))
check(b"\x1b[?1049h" in s.raw, "program entered the alt screen")
rows = left(f)
check(any("acme" in r for r in rows) and any("globex" in r for r in rows), "tickets are grouped by stack: %r" % rows)
check(sum("PLAT-" in r or "SHOP-" in r for r in rows) == 3, "every cached ticket is listed")
body = "\n".join(f)
check("PLAT-2099" in body and "Context" in body and "add the guard" in body, "preview renders the description")
check("rgb:" not in f[1], "the background reply is not typed into the filter")
# SGR press+release on the third list line (acme, PLAT-2099, PLAT-2098): 1-based column 5, line 3+2+1
f = s.send(b"\x1b[<0;5;6M\x1b[<0;5;6m", 0.5)
rows = left(f)
check(any(r.startswith("▌") and "PLAT-2098" in r for r in rows) and s.proc.poll() is None,
      "a click selects the ticket without opening it: %r" % rows)
f = s.send(b"602", 0.6); dump("filtered", f)
rows = left(f)
check(any("SHOP-602" in r for r in rows) and not any("PLAT-" in r for r in rows), "a ticket number filters: %r" % rows)
check(counter(f) == "1/4", "the counter follows the filter: %r" % counter(f))
s.send(ENTER, 0.3)
check(s.finish() == 0, "clean exit after enter")
check(opened() == ["https://globex.example/browse/SHOP-602"], "enter opens the ticket in the browser: %r" % opened())

# ---------- run 1b: a GitHub issue sits next to the Jira tickets ----------
s = session()
f = s.start("asgotoissues ❯")
rows = left(f)
check(any(r == "home" for r in rows) and any("tool#12" in r for r in rows), "the github stack has its group: %r" % rows)
f = s.send(b"tool#12", 0.8); dump("github issue", f)
rows = left(f)
check(any(r.startswith("▌") and "tool#12" in r for r in rows) and counter(f) == "1/4", "a repo#number filters: %r" % rows)
body = "\n".join(f)
check("[open]" in body and "me/tool · bug, in progress" in body, "the preview meta line is the provider's")
check("snake_case_name" in body and "keep the bullet" in body, "a markdown body skips the wiki conversion")
s.send(ENTER, 0.3)
check(s.finish() == 0 and opened() == ["https://github.com/me/tool/issues/12"], "enter opens the github issue: %r" % opened())

# ---------- run 2: q quits with an empty filter ----------
s = session()
s.start("asgotoissues ❯")
os.write(s.master, b"q"); s.pump(0.4)
check(s.finish() == 0 and opened() == [], "q quits with an empty filter and opens nothing")

# ---------- run 3: esc cancels and opens nothing ----------
s = session()
s.start("asgotoissues ❯")
s.send(DOWN, 0.3)
os.write(s.master, ESC); s.pump(0.4)
check(s.finish() == 0 and opened() == [], "esc quits and opens nothing")

# ---------- run 4: the divider moves and stays where it was left ----------
s = session()
f = s.start("asgotoissues ❯"); at = divider(f)
f = s.send(SHIFT_RIGHT, 0.6); grown = divider(f)
check(grown > at and all(len(l) == COLS for l in f), "shift+right grows the list: %d -> %d" % (at, grown))
f = s.send(SHIFT_LEFT, 0.6)
check(divider(f) == at, "shift+left shrinks it back: %d" % divider(f))
s.send(SHIFT_RIGHT, 0.6)
os.write(s.master, ESC); s.pump(0.4); s.finish()
s = session()
f = s.start("asgotoissues ❯")
check(divider(f) == grown, "the next run opens with the same split: %d" % divider(f))
s.send(SHIFT_LEFT, 0.6)
os.write(s.master, ESC); s.pump(0.4); s.finish()

done()
