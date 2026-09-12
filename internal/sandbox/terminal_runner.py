"""Executed ONLY inside a sandbox by its native PTY process API.

Requires Linux /proc, prctl and Python 3. The supervisor is outside its child's
rlimits so exhaustion cannot prevent cleanup. RLIMIT_AS is per-process; CPU
and RSS also have aggregate, sampled command-tree budgets. RSS counts shared
pages per process and sampling can overshoot; this is not a container quota.
Container isolation remains the security boundary, including for sandbox root.
"""
import ctypes
import fcntl
import os
import resource
import signal
import struct
import sys
import termios
import time

token, wall, cpu, memory, cols, rows, command = sys.argv[1:]
wall, cpu, memory = float(wall), int(cpu), int(memory)
libc = ctypes.CDLL(None, use_errno=True)
if libc.prctl(36, 1, 0, 0, 0) != 0:  # PR_SET_CHILD_SUBREAPER
    raise OSError(ctypes.get_errno(), "terminal subreaper unavailable")
if not all(os.isatty(fd) for fd in (0, 1, 2)):
    raise RuntimeError("terminal runner requires a real PTY")
fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", int(rows), int(cols), 0, 0))
signal.signal(signal.SIGTTOU, signal.SIG_IGN)
signal.signal(signal.SIGTTIN, signal.SIG_IGN)
stopping = 0
# Reserved supervisor result, not 137: ordinary SIGKILL is not proof of a limit.
MEMORY_LIMIT_EXIT = 200


def stop(sig, frame):
    global stopping
    stopping = sig


for sig in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT):
    signal.signal(sig, stop)


def processes():
    result = {}
    for name in os.listdir("/proc"):
        if not name.isdigit():
            continue
        try:
            with open("/proc/" + name + "/stat") as src:
                fields = src.read().rsplit(")", 1)[1].split()
            result[int(name)] = (int(fields[1]), fields[19],
                                sum(int(fields[i]) for i in (11, 12, 13, 14)),
                                int(fields[21]))  # RSS in pages, /proc stat field 24
        except (OSError, ValueError, IndexError):
            pass
    return result


def descendants(table):
    found = {os.getpid()}
    while True:
        added = {pid for pid, info in table.items() if info[0] in found} - found
        if not added:
            return found - {os.getpid()}
        found.update(added)


def kill_tree(sig):
    table = processes()
    for pid in descendants(table):
        try:
            os.kill(pid, sig)
        except ProcessLookupError:
            pass
    try:
        os.killpg(child, sig)
    except ProcessLookupError:
        pass


cancel_path = "/tmp/" + token + ".cancel"
if stopping or os.path.lexists(cancel_path):
    try:
        os.unlink(cancel_path)
    except FileNotFoundError:
        pass
    sys.exit(143)

child = os.fork()
if child == 0:
    try:
        os.setpgid(0, 0)
        os.tcsetpgrp(0, os.getpid())
        for sig in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT,
                    signal.SIGTTOU, signal.SIGTTIN, signal.SIGPIPE):
            signal.signal(sig, signal.SIG_DFL)
        resource.setrlimit(resource.RLIMIT_CPU, (cpu, cpu + 1))
        resource.setrlimit(resource.RLIMIT_AS, (memory, memory))
        resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
        os.execve("/bin/bash", ["/bin/bash", "-lc", command], os.environ)
    except BaseException as exc:
        print("terminal runner: " + str(exc), file=sys.stderr, flush=True)
        os._exit(125)

try:
    os.setpgid(child, child)
except (ProcessLookupError, PermissionError):
    pass
deadline = time.monotonic() + wall
ticks = os.sysconf("SC_CLK_TCK")
page_size = os.sysconf("SC_PAGE_SIZE")
code = 125
try:
    while True:
        pid, status = os.waitpid(child, os.WNOHANG)
        if pid:
            code = os.waitstatus_to_exitcode(status)
            code = code if code >= 0 else 128 - code
            if code == MEMORY_LIMIT_EXIT:
                code = 1  # A command cannot claim the supervisor's memory result.
            break
        if stopping:
            code = 128 + stopping
            if stopping == signal.SIGINT:
                kill_tree(signal.SIGINT)
                time.sleep(0.2)
            break
        if time.monotonic() >= deadline:
            code = 124
            break
        table = processes()
        tree = descendants(table)
        usage = resource.getrusage(resource.RUSAGE_CHILDREN)
        consumed = usage.ru_utime + usage.ru_stime
        consumed += sum(table[pid][2] / ticks for pid in tree)
        if consumed >= cpu:
            code = 152
            break
        if sum(table[pid][3] for pid in tree) * page_size >= memory:
            code = MEMORY_LIMIT_EXIT
            break
        try:
            os.utime("/var/lib/weknora-sandbox-activity", None)
        except OSError:
            pass
        time.sleep(0.025)
finally:
    # Re-scan while killing: setsid/double-fork children are adopted here,
    # rather than being lost when the original shell disappears.
    end = time.monotonic() + 3
    while True:
        kill_tree(signal.SIGKILL)
        try:
            while os.waitpid(-1, os.WNOHANG)[0]:
                pass
        except ChildProcessError:
            break
        if time.monotonic() >= end:
            code = 125  # Do not claim successful cleanup of unkillable tasks.
            break
        time.sleep(0.02)
    try:
        os.unlink(cancel_path)
    except FileNotFoundError:
        pass
sys.exit(code)
