#!/usr/bin/env python3
"""End-to-end TUI check for asgotoissues without a real terminal.

Spawns the binary on a pty, answers the terminal queries bubbletea sends
(OSC 10/11, CSI 6n, DA1), replays keystrokes, and asserts on frames rendered
with pyte. Everything runs in a throwaway sandbox: a fake HOME, a synthetic
config (ASGOTOISSUES_CONFIG), an empty netrc, a fresh synthetic ticket cache (so
nothing is fetched) and logging stubs instead of the browser
(ASGOTOISSUES_OPENER) and the clipboard (ASGOTOISSUES_CLIPBOARD). It never reads the real config and never talks to Jira
or GitHub (gh does not have to be installed).

Usage: scripts/pty-check.py ./asgotoissues   (needs python3 + pyte)
"""
NAME, ROWS, COLS = "asgotoissues", 28, 150
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
def ticket(key, stack, summary, status, cat, desc="", parent="", ghost=False):
    t = {"key": key, "stack": stack, "url": "https://%s.example/browse/%s" % (stack, key), "summary": summary,
         "description": desc, "status": status, "status_cat": cat, "type": "Task", "priority": "Medium",
         "project": key.split("-")[0], "created": now, "updated": now}
    if parent:
        t["parent_key"], t["parent_summary"] = parent, "the parent"
        t["parent_url"] = "https://%s.example/browse/%s" % (stack, parent)
    if ghost: t["ghost"] = True
    return t
def pr(repo, n, title, state, stack, ref, **extra):
    p = {"url": "https://github.com/%s/pull/%d" % (repo, n), "number": n, "repo": repo, "stack": stack,
         "key": "%s#%d" % (repo.split("/")[1], n), "title": title, "body": "the *body*", "state": state,
         "head": "feat/%s" % ref.lower(), "base": "main", "author": "me", "mine": True, "refs": [ref],
         "created": now, "updated": now}
    p.update(extra)
    return p
tickets = [
    ticket("PLAT-2099", "acme", "Enforce authz on the export endpoint", "In Progress", "In Progress",
           "h2. Context\n\nThe *export* endpoint skips the policy check.\n\n* add the guard\n* cover it with a test"),
    ticket("PLAT-2098", "acme", "Audit trail at the service boundary", "To Do", "To Do"),
    # a sub-task under an epic that is not mine: the epic is a ghost the provider fetched
    ticket("SHOP-602", "globex", "Create test fixtures", "Open", "To Do", parent="SHOP-600"),
    ticket("SHOP-600", "globex", "Test the shop", "Open", "To Do", ghost=True),
    # a GitHub issue: its own source, a Markdown body, the meta line comes with it
    {"key": "tool#12", "stack": "home", "source": "github", "url": "https://github.com/me/tool/issues/12",
     "summary": "Cache layer rewrite", "description": "Rename `snake_case_name`.\n\n* keep the bullet",
     "body_format": "markdown", "status": "open", "state": "doing", "status_cat": "", "type": "", "priority": "",
     "project": "me/tool", "meta": ["me/tool", "bug, in progress"], "created": now, "updated": now},
]
# two PRs under PLAT-2099, in two repos: one open with conflicts, one merged
pulls = [
    pr("acme/front", 41, "PLAT-2099 guard the export", "OPEN", "acme", "PLAT-2099", mergeable="CONFLICTING"),
    pr("acme/infra", 7, "PLAT-2099 policy config", "MERGED", "acme", "PLAT-2099"),
]
state = os.path.join(home, ".local", "state", "herdr", "plugins", "asumaran.asgotoissues")
write(os.path.join(state, "issuecache.json"), json.dumps({"fetched_at": now, "issues": tickets, "pulls": pulls}))
# everything listed (a fresh state dir shows the work going on only); no short
# titles (nothing is spawned); no checkouts read
write(os.path.join(state, "show"), "all\n")
write(os.path.join(state, "titles"), "original\n")
open_log = os.path.join(SANDBOX, "open.log")
opener = write(os.path.join(SANDBOX, "opener"), '#!/bin/sh\nprintf "%%s\\n" "$1" >> "%s"\n' % open_log, 0o755)
clip_log = os.path.join(SANDBOX, "clip.log")
clipboard = write(os.path.join(SANDBOX, "clipboard"), '#!/bin/sh\ncat > "%s"\n' % clip_log, 0o755)

def session():
    env = dict(os.environ, TERM="xterm-256color", COLORTERM="truecolor", HOME=home, NETRC=netrc,
               ASGOTOISSUES_CONFIG=config, ASGOTOISSUES_OPENER=opener, ASGOTOISSUES_CLIPBOARD=clipboard,
               XDG_CONFIG_HOME=os.path.join(home, ".config"), ASGOTOISSUES_CHECKOUTS=SANDBOX,
               ASGOTOISSUES_NO_SUMMARIES="1")
    for k in ("HERDR_PLUGIN_STATE_DIR", "XDG_STATE_HOME", "JIRA_TOKEN_ACME", "JIRA_TOKEN_GLOBEX"):
        env.pop(k, None)
    if os.path.exists(open_log): os.remove(open_log)
    if os.path.exists(clip_log): os.remove(clip_log)
    return Session(env)

def opened():
    time.sleep(0.3)
    return open(open_log).read().splitlines() if os.path.exists(open_log) else []

def copied():
    return open(clip_log).read() if os.path.exists(clip_log) else ""

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

# The panel: f1 lays the options (the order, the PRs shown) and the keys over the frame, takes the keys, and esc closes it.
p = s.send(b"\x1bOP", 0.6); dump("panel", p)
check(len(p) == len(f) and any("╭─ options " in l for l in p) and any("Keys" in l for l in p) and any("Order" in l and "‹created›" in l for l in p)
      and any("PRs" in l and "‹all›" in l for l in p) and any("Rows" in l and "‹two lines›" in l for l in p)
      and any("Show" in l and "‹all›" in l for l in p) and any("Group" in l and "‹tree›" in l for l in p),
      "f1 opens the panel over a frame that keeps its size")
s.send(b"zz", 0.6); p = s.send(b"\x1b", 0.6)
check(s.proc.poll() is None and not any("╭─ options " in l for l in p) and prompt(p) == prompt(f), "esc closes the panel, which took the keys: %r" % prompt(p))
p = s.send(b"?", 0.6)
check(prompt(p).endswith("?"), "? is text for the filter: %r" % prompt(p))
f = s.send(b"\x7f", 0.6)
check(prompt(f) == "asgotoissues ❯ Search by title, key, status, repo, PR…", "prompt line is clean: %r" % f[1])
check(devmark(f), "a dev build says so on the edge over the input")
check(f[0].startswith("╭") and f[-1].startswith("╰") and "┬" in f[2],
      "one frame: input right under the top border, no title line")
check(counter(f) == "4/4" and "type filter" in f[-2] and "esc/q quit" in f[-2], "counter %r (my tickets: no ghost, no PR) and help %r" % (counter(f), f[-2]))
check(b"\x1b[?1049h" in s.raw, "program entered the alt screen")
rows = left(f)
check(any(r == "▼ acme" for r in rows) and any(r == "▼ globex" for r in rows), "tickets are grouped by stack, each header with its arrow: %r" % rows)
check(sum(re.search(r"(plat|shop)-[0-9]+ ", r) is not None for r in rows) == 4, "every cached ticket is listed, the ghost epic too, keys in lower case: %r" % rows)
title = next(r for r in rows if r.startswith("▌▼ plat-2099 "))
detail = next((r for r in rows if r.startswith("▌") and "in progress · today" in r), "")
check(detail and detail.index("in progress") == title.index("plat-2099") + 4, "the story's details start 4 cells past its key, selected too: %r" % rows)
prl = next((r for r in rows if "○ front#41 PLAT-2099" in r), "")
prd = next((r for r in rows if r.lstrip().startswith("in review · conflicts")), "")
check(prl and prl.index("○") == title.index("plat-2099") + 2 and prd and prd.index("in review") == prl.index("front#41")
      and any("○ infra#7 PLAT-2099" in r for r in rows) and any(r.lstrip().startswith("merged · ") for r in rows),
      "the PRs are bullets of their ticket, their keys where its details start, their details under the key: %r" % rows)
epic = next((r for r in rows if r.startswith(" ▼ shop-600 ")), "")
sub = next((r for r in rows if "shop-602" in r), "")
check(epic and sub.startswith(" └──── shop-602") and sub.index("shop-602") == epic.index("shop-600") + 4, "the sub-task hangs from the ghost epic's arrow: %r" % rows)
body = "\n".join(f)
check("PLAT-2099" in body and "Context" in body and "add the guard" in body, "preview renders the description")
check("rgb:" not in f[1], "the background reply is not typed into the filter")
# SGR press+release on the eighth list line (acme; PLAT-2099, ↳front#41, ↳infra#7 on two lines each; PLAT-2098): 1-based column 5, line 3+7+1
f = s.send(b"\x1b[<0;5;11M\x1b[<0;5;11m", 0.5)
rows = left(f)
check(any(r.startswith("▌") and "plat-2098" in r for r in rows) and s.proc.poll() is None,
      "a click selects the ticket without opening it: %r" % rows)
f = s.send(b"\x1b[<64;5;11M", 0.5)   # the wheel, up, over the list
rows = left(f)
check(any(r.startswith("▌") and "infra#7" in r for r in rows) and s.proc.poll() is None,
      "the wheel over the list moves the cursor, onto a PR row too: %r" % rows)
body = "\n".join(f)
check("infra#7 · feat/plat-2099 → main · by me" in body and any("│ merged" in l for l in f), "a PR row previews the PR's facts: %r" % [l for l in f if "infra#7" in l])
f = s.send(b"602", 0.6); dump("filtered", f)
rows = left(f)
check(any(r.startswith("▌") and "shop-602" in r for r in rows) and not any("plat-" in r for r in rows), "a ticket number filters: %r" % rows)
check(any("shop-600" in r and not r.startswith("▌") for r in rows), "the hit keeps the parent that leads to it, as context: %r" % rows)
check(counter(f) == "1/4", "the counter follows the filter: %r" % counter(f))
s.send(ENTER, 0.3)
check(s.finish() == 0, "clean exit after enter")
check(opened() == ["https://globex.example/browse/SHOP-602"], "enter opens the ticket in the browser: %r" % opened())

# ---------- run 1b: a GitHub issue sits next to the Jira tickets ----------
s = session()
f = s.start("asgotoissues ❯")
rows = left(f)
check(any(r == "▼ home" for r in rows) and any("tool#12" in r for r in rows) and any(r.lstrip().startswith("in progress · no PR") for r in rows),
      "the github stack has its group, the issue's status from its labels: %r" % rows)
f = s.send(b"tool#12", 0.8); dump("github issue", f)
rows = left(f)
check(any(r.startswith("▌") and "tool#12" in r for r in rows) and counter(f) == "1/4", "a repo#number filters: %r" % rows)
body = "\n".join(f)
check("[open]" in body and "me/tool · bug, in progress" in body, "the preview meta line is the provider's")
check("snake_case_name" in body and "keep the bullet" in body, "a markdown body skips the wiki conversion")
s.send(ENTER, 0.3)
check(s.finish() == 0 and opened() == ["https://github.com/me/tool/issues/12"], "enter opens the github issue: %r" % opened())

# ---------- run 1c: ctrl+y copies the key of the issue under the cursor ----------
s = session()
s.start("asgotoissues ❯")
s.send(b"602", 0.6)
f = s.send(b"\x19", 0.6); dump("copied", f)
check(copied() == "SHOP-602", "ctrl+y copies the issue key: %r" % copied())
check("copied SHOP-602" in f[-2], "the help line confirms the copy: %r" % f[-2])
check(prompt(f).endswith("602") and s.proc.poll() is None, "ctrl+y leaves the filter alone and keeps the list open: %r" % f[1])
os.write(s.master, ESC); s.pump(0.4)
check(s.finish() == 0 and opened() == [], "esc quits after a copy and opens nothing")

# ---------- run 1d: enter on a PR row opens the PR; ctrl+s cycles the order and the next run keeps it ----------
s = session()
s.start("asgotoissues ❯")
f = s.send(DOWN, 0.4)
rows = left(f)
check(any(r.startswith("▌") and "○ front#41 PLAT-2099" in r for r in rows) and any(r.startswith("▌") and r.lstrip("▌ ").startswith("in review · con") for r in rows),
      "down from the story lands on its PR, both lines selected: %r" % rows)
f = s.send(CTRL_S, 0.6); dump("order", f)
check("order: updated" in f[-2], "ctrl+s says the order it set: %r" % f[-2])
s.send(ENTER, 0.3)
check(s.finish() == 0 and opened() == ["https://github.com/acme/front/pull/41"], "enter opens the PR in the browser: %r" % opened())
s = session()
p = s.start("asgotoissues ❯")
p = s.send(b"\x1bOP", 0.6)
check(any("Order" in l and "‹updated›" in l for l in p), "the next run opens with the saved order: %r" % [l for l in p if "Order" in l])
s.send(b"\x1b", 0.4)
for _ in range(3): s.send(CTRL_S, 0.3)   # back to created, so the runs after this one start where they expect
os.write(s.master, ESC); s.pump(0.4); s.finish()

# ---------- run 1e: space folds a ticket, and unfolds it ----------
s = session()
f = s.start("asgotoissues ❯")
f = s.send(b" ", 0.6); dump("folded", f)
rows = left(f)
check(any(r.startswith("▌▶") and "plat-2099" in r for r in rows) and not any("front#41" in r for r in rows) and any("plat-2098" in r for r in rows),
      "space folds the story's PRs away and turns its arrow: %r" % rows)
f = s.send(b" ", 0.6)
rows = left(f)
check(any("front#41" in r for r in rows) and not any("▶" in r for r in rows), "space again unfolds it: %r" % rows)
check(prompt(f) == "asgotoissues ❯ Search by title, key, status, repo, PR…", "space with an empty filter is not typed: %r" % f[1])
os.write(s.master, ESC); s.pump(0.4); s.finish()

# ---------- run 1f: shift+tab folds the tree a level, ctrl+g groups by phase, ctrl+t cycles the show ----------
s = session()
f = s.start("asgotoissues ❯")
f = s.send(b"\x1b[Z", 0.6); dump("level", f)
rows = left(f)
check("level 1" in f[-2] and not any("front#41" in r or "shop-602" in r for r in rows) and any("plat-2098" in r for r in rows),
      "shift+tab shows the roots alone, folded: %r" % rows)
f = s.send(b"\t", 0.6)
check("all levels" in f[-2] and any("shop-602" in r for r in left(f)), "tab unfolds everything again: %r" % f[-2])
f = s.send(b"\x07", 0.6); dump("phase", f)
rows = left(f)
check(any("── PR in review (1)" in r for r in rows) and any("plat-2099 › front#41" in r for r in rows) and any("── no PR" in r for r in rows),
      "ctrl+g lists by phase, each PR with its path: %r" % rows)
f = s.send(b"\x07", 0.6)
check(any(r.startswith("▌▼ plat-2099") or "▼ plat-2099" in r for r in left(f)), "ctrl+g again is the tree: %r" % left(f))
f = s.send(b"\x14", 0.6)
rows = left(f)
check("show: working" in f[-2] and not any("plat-2098" in r for r in rows) and any("plat-2099" in r for r in rows),
      "ctrl+t shows the work going on: the ticket not started goes: %r" % rows)
for _ in range(2): s.send(b"\x14", 0.3)   # back to all for the runs after this one
os.write(s.master, ESC); s.pump(0.4); s.finish()

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
