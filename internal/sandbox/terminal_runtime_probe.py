"""Versioned workbench runtime contract probe, executed inside the sandbox."""

import ctypes
import errno
import fcntl  # noqa: F401 - importing is part of the terminal contract
import json
import os
import resource  # noqa: F401 - imported by both runtime helpers
import stat
import termios  # noqa: F401 - importing is part of the terminal contract

CONTRACT = "weknora-workbench-runtime/v1"
ROOT = "/workspace/output"
STAGING_PREFIX = ".weknora-workbench-runtime-"
DIR_FLAGS = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC
FILE_FLAGS = os.O_RDWR | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC
PR_SET_CHILD_SUBREAPER = 36
PR_GET_CHILD_SUBREAPER = 37
RENAME_NOREPLACE = 1


def check_bash():
    return os.path.isfile("/bin/bash") and os.access("/bin/bash", os.X_OK)


def check_proc():
    names = os.listdir("/proc")
    if str(os.getpid()) not in names:
        return False
    fd = os.open("/proc/self/stat", os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    os.close(fd)
    fd = os.open("/proc/self/fd", DIR_FLAGS)
    os.close(fd)
    return True


def check_prctl():
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.prctl(PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) != 0:
        return False
    current = ctypes.c_int()
    if libc.prctl(PR_GET_CHILD_SUBREAPER, ctypes.byref(current), 0, 0, 0) != 0:
        return False
    return current.value == 1


def check_renameat2():
    libc = ctypes.CDLL(None, use_errno=True)
    renameat2 = getattr(libc, "renameat2", None)
    if renameat2 is None:
        return False
    renameat2.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int,
                          ctypes.c_char_p, ctypes.c_uint]
    renameat2.restype = ctypes.c_int
    directory = os.open(ROOT, DIR_FLAGS)
    nonce = os.urandom(16).hex()
    source = STAGING_PREFIX + nonce + "-source"
    target = STAGING_PREFIX + nonce + "-target"
    source_fd = -1
    try:
        source_fd = os.open(source, FILE_FLAGS, 0o600, dir_fd=directory)
        source_info = os.fstat(source_fd)
        target_fd = os.open(target, FILE_FLAGS, 0o600, dir_fd=directory)
        os.close(target_fd)
        if renameat2(directory, os.fsencode(source), directory,
                     os.fsencode(target), RENAME_NOREPLACE) == 0:
            return False
        if ctypes.get_errno() != errno.EEXIST:
            return False
        os.unlink(target, dir_fd=directory)
        if renameat2(directory, os.fsencode(source), directory,
                     os.fsencode(target), RENAME_NOREPLACE) != 0:
            return False
        target_fd = os.open(target, os.O_PATH | os.O_NOFOLLOW | os.O_CLOEXEC,
                            dir_fd=directory)
        try:
            target_info = os.fstat(target_fd)
            return (stat.S_ISREG(target_info.st_mode)
                    and (source_info.st_dev, source_info.st_ino)
                    == (target_info.st_dev, target_info.st_ino))
        finally:
            os.close(target_fd)
    finally:
        if source_fd >= 0:
            os.close(source_fd)
        for name in (source, target):
            try:
                os.unlink(name, dir_fd=directory)
            except FileNotFoundError:
                pass
        os.close(directory)


TERMINAL_CHECKS = (
    ("bash", check_bash),
    ("proc", check_proc),
    ("prctl", check_prctl),
)

FILE_CHECKS = (
    ("proc", check_proc),
    ("renameat2", check_renameat2),
)


def probe(checks):
    for name, check in checks:
        try:
            if not check():
                return name
        except (AttributeError, OSError, RuntimeError, ValueError):
            return name
    return ""


def main():
    missing_terminal = probe(TERMINAL_CHECKS)
    missing_files = probe(FILE_CHECKS)
    reply = {
        "contract": CONTRACT,
        "terminal": not missing_terminal,
        "files": not missing_files,
    }
    if missing_terminal:
        reply["missing_terminal"] = missing_terminal
    if missing_files:
        reply["missing_files"] = missing_files
    print(json.dumps(reply, ensure_ascii=True, separators=(",", ":")))


if __name__ == "__main__":
    main()
