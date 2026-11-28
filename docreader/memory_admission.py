import io
import logging
import os
import threading
import zipfile
from contextlib import contextmanager
from pathlib import Path

logger = logging.getLogger(__name__)
MIB = 1024 * 1024
MEMORY_CAPACITY_WAIT = "DOCREADER_MEMORY_CAPACITY_WAIT"


class MemoryCapacityWait(Exception):
    """The caller should retry admission after other documents drain."""


def container_memory_working_set(
    root: Path = Path("/sys/fs/cgroup"),
    membership: Path = Path("/proc/self/cgroup"),
) -> int:
    for line in membership.read_text().splitlines():
        _, controllers, path = line.split(":", 2)
        group = path.lstrip("/")
        if not controllers:
            usage_file = root / group / "memory.current"
            inactive_key = "inactive_file"
        elif "memory" in controllers.split(","):
            usage_file = root / "memory" / group / "memory.usage_in_bytes"
            inactive_key = "total_inactive_file"
        else:
            continue
        if usage_file.is_file():
            break
    else:
        raise OSError("process memory cgroup is unavailable")
    usage = int(usage_file.read_text())
    # Reclaimable page cache should not keep a drained parser paused.
    try:
        with (usage_file.parent / "memory.stat").open() as stats:
            for line in stats:
                key, value = line.split()
                if key == inactive_key:
                    return max(0, usage - int(value))
    except (OSError, ValueError):
        pass
    return usage


def estimate_parse_memory(file_name: str, file_type: str, size: int, source=None) -> int:
    is_xlsx = (
        file_type.strip().lower().lstrip(".") == "xlsx"
        or os.path.splitext(file_name)[1].lower() == ".xlsx"
    )
    if is_xlsx:
        if source is not None:
            if isinstance(source, bytes):
                source = io.BytesIO(source)
            try:
                with zipfile.ZipFile(source) as workbook:
                    size = max(size, sum(entry.file_size for entry in workbook.infolist()))
            except (OSError, ValueError, zipfile.BadZipFile):
                # Preserve the parser's handling of damaged or mislabeled workbooks.
                pass
        return 128 * MIB + 8 * size
    return 64 * MIB + 2 * size


class MemoryAdmission:
    def __init__(self, pause_mib: int, resume_mib: int, read_usage=container_memory_working_set):
        self.pause = pause_mib * MIB
        self.resume = resume_mib * MIB
        self._read_usage = read_usage
        self._lock = threading.Lock()
        self._reserved = 0
        self._paused = False
        self._metrics_unavailable = False
        if self.pause:
            try:
                self._read_usage()
            except (OSError, ValueError) as exc:
                raise ValueError("DocReader memory admission requires readable memory cgroup metrics") from exc

    def reserve(self, estimate: int) -> int:
        if not self.pause:
            return 0
        # Estimates are scheduling weights. A large supported file must have
        # a chance to run on a quiet node, even when its estimate is very high.
        estimate = min(estimate, self.pause // 2)
        with self._lock:
            try:
                usage = self._read_usage()
            except (OSError, ValueError) as exc:
                if not self._metrics_unavailable:
                    logger.error("DocReader cannot read container memory usage: %s", exc)
                self._metrics_unavailable = True
                raise MemoryCapacityWait from exc
            self._metrics_unavailable = False

            if usage >= self.pause and not self._paused:
                self._paused = True
                logger.info("DocReader paused admission at %d MiB", usage // MIB)
            elif self._paused and usage <= self.resume:
                self._paused = False
                logger.info("DocReader resumed admission at %d MiB", usage // MIB)

            # Reserve before the parser starts allocating. Counting live working
            # set and reservations together errs on the side of keeping headroom.
            reserved = self._reserved + estimate
            if self._paused or usage + reserved > self.pause:
                raise MemoryCapacityWait
            self._reserved = reserved
            return estimate

    def reserve_request(self, file_name: str, file_type: str, size: int, source=None) -> int:
        if not self.pause:
            return 0
        return self.reserve(estimate_parse_memory(file_name, file_type, size, source))

    @contextmanager
    def hold(self, reservation: int):
        try:
            yield
        finally:
            self.release(reservation)

    def release(self, reservation: int) -> None:
        if reservation:
            with self._lock:
                self._reserved -= reservation
