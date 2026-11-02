#!/usr/bin/env python3
"""Export optional aggregate host metrics for the administrator runtime page."""
import argparse
import json
import os
import pathlib
import subprocess
import tempfile
import time


def command(args):
    return subprocess.run(args, capture_output=True, text=True, timeout=10, check=True).stdout


def disk(path):
    try:
        stat = os.statvfs(path)
        used = (stat.f_blocks - stat.f_bfree) * stat.f_frsize
        available = stat.f_bavail * stat.f_frsize
        return {"name": path, "available": True, "total_bytes": stat.f_blocks * stat.f_frsize,
                "available_bytes": available, "used_percent": round(100 * used / (used + available), 1) if used + available else 0}
    except OSError:
        return {"name": path, "available": False}


def backup(name, directory, now):
    root = pathlib.Path(directory)
    result = {"name": name, "available": False, "snapshot_count": 0}
    try:
        result["snapshot_count"] = sum(p.is_dir() and not p.is_symlink() for p in root.iterdir())
        latest = (root / "latest").resolve(strict=True)
        if latest.parent != root.resolve() or not (latest / "COMPLETE").is_file():
            return result
        completed = int((latest / "COMPLETE").stat().st_mtime)
        if completed > now + 60:
            return result
        result.update(available=True, completed_at=completed, stale=now - completed > 26 * 3600)
    except (OSError, RuntimeError):
        pass
    return result


def service(unit, running_required):
    try:
        data = dict(line.split("=", 1) for line in command([
            "systemctl", "show", unit, "-p", "LoadState", "-p", "ActiveState", "-p", "Result", "-p", "ExecMainStatus"
        ]).splitlines() if "=" in line)
        if data.get("LoadState") != "loaded":
            return {"name": unit, "available": False}
        return {"name": unit, "available": True, "active": data.get("ActiveState"),
                "result": data.get("Result"), "exit_status": int(data["ExecMainStatus"]), "running_required": running_required}
    except (OSError, ValueError, KeyError, subprocess.SubprocessError):
        return {"name": unit, "available": False}


def container_log(name):
    try:
        container = json.loads(command(["docker", "inspect", "--", name]))[0]
        log_path = container.get("LogPath")
        size = None
        if log_path:
            path = pathlib.Path(log_path)
            if path.is_file():
                # Include rotated siblings; never read log contents.
                size = sum(p.stat().st_size for p in [path, *path.parent.glob(path.name + ".*")] if p.is_file())
        options = container["HostConfig"].get("LogConfig", {}).get("Config", {})
        return {"name": name, "bytes": size, "max_size": options.get("max-size"), "max_files": options.get("max-file"),
                "status": container["State"].get("Status"), "health": container["State"].get("Health", {}).get("Status")}
    except (OSError, ValueError, KeyError, IndexError, subprocess.SubprocessError):
        return {"name": name}


def collect(disks=None, backups=None, containers=None, services=None, running_services=None):
    now = int(time.time())
    memory = {}
    try:
        for line in pathlib.Path("/proc/meminfo").read_text().splitlines():
            key, value = line.split(":", 1)
            if key in ("MemTotal", "MemAvailable"):
                memory[key] = int(value.split()[0]) * 1024
    except (OSError, ValueError):
        pass
    host = {"cpu_count": os.cpu_count(), "load_1m": os.getloadavg()[0],
            "memory_total_bytes": memory.get("MemTotal"), "memory_available_bytes": memory.get("MemAvailable")}
    return {"collected_at": now, "host": host, "disks": [disk(p) for p in (disks or ["/"])],
            "backups": [backup(name, path, now) for name, path in (backups or [])],
            "logs": [container_log(name) for name in (containers or [])],
            "services": [service(unit, False) for unit in (services or [])] +
                        [service(unit, True) for unit in (running_services or [])]}


def write_atomic(path, status):
    target = pathlib.Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    fd, temp = tempfile.mkstemp(prefix=".status-", dir=target.parent)
    try:
        with os.fdopen(fd, "w") as output:
            json.dump(status, output, ensure_ascii=False, allow_nan=False)
            output.flush()
            os.fsync(output.fileno())
        os.chmod(temp, 0o644)
        os.replace(temp, target)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)


def backup_arg(value):
    name, sep, path = value.partition("=")
    if not sep or not name or not path:
        raise argparse.ArgumentTypeError("expected NAME=DIRECTORY")
    return name, path


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True)
    parser.add_argument("--disk", action="append")
    parser.add_argument("--backup", type=backup_arg, action="append")
    parser.add_argument("--container", action="append")
    parser.add_argument("--service", action="append", help="Completed scheduled job; inactive is allowed")
    parser.add_argument("--running-service", action="append", help="Service that must remain active")
    args = parser.parse_args()
    write_atomic(args.output, collect(args.disk, args.backup, args.container, args.service, args.running_service))
