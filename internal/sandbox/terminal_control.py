"""Sandbox-only control of one server-generated terminal runner identity."""
import os
import signal
import sys
import time

token, operation = sys.argv[1:]

# Attach can be acknowledged before the runner becomes visible in /proc.
# Leave a zero-byte tombstone until that runner consumes it or the sandbox is
# reclaimed. A missing process must not turn a canceled start into a late exec.
if operation != "interrupt":
    try:
        fd = os.open("/tmp/" + token + ".cancel",
                     os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        os.close(fd)
    except FileExistsError:
        pass


def snapshot():
    result = {}
    for name in os.listdir("/proc"):
        if not name.isdigit():
            continue
        try:
            with open("/proc/" + name + "/cmdline", "rb") as src:
                args = src.read().split(b"\0")
            with open("/proc/" + name + "/stat") as src:
                fields = src.read().rsplit(")", 1)[1].split()
            result[int(name)] = (int(fields[1]), fields[19], fields[0], args)
        except (OSError, ValueError, IndexError):
            pass
    return result


def send(pid, identity, sig):
    current = snapshot().get(pid)
    if current and current[1] == identity:
        try:
            os.kill(pid, sig)
        except ProcessLookupError:
            pass


table = snapshot()
targets = {pid: info[1] for pid, info in table.items()
           if len(info[3]) == 13 and info[3][5] == token.encode()
           and info[3][1:4] == [b"-I", b"-u", b"-c"]}
sig = signal.SIGINT if operation == "interrupt" else signal.SIGTERM
for pid, identity in targets.items():
    send(pid, identity, sig)
if operation == "interrupt":
    sys.exit(0)

deadline = time.monotonic() + 4
while targets:
    table = snapshot()
    targets = {pid: identity for pid, identity in targets.items()
               if pid in table and table[pid][1] == identity and table[pid][2] != "Z"}
    if not targets:
        break
    if time.monotonic() >= deadline:
        # A stopped/unresponsive supervisor cannot run its cleanup handler.
        # Stop its descendants before killing it, including new process groups.
        found = set(targets)
        while True:
            added = {pid for pid, info in table.items() if info[0] in found} - found
            if not added:
                break
            found.update(added)
        for pid in found - set(targets):
            send(pid, table[pid][1], signal.SIGKILL)
        for pid, identity in targets.items():
            send(pid, identity, signal.SIGKILL)
        sys.exit(1)  # Cleanup could not be acknowledged by the supervisor.
    time.sleep(0.025)
