import logging
import threading
from contextlib import contextmanager
from typing import Dict, Iterator

logger = logging.getLogger(__name__)

_LIMITERS: Dict[str, threading.BoundedSemaphore] = {}
_LIMITERS_LOCK = threading.Lock()


def _get_limiter(name: str, max_workers: int) -> threading.BoundedSemaphore:
    with _LIMITERS_LOCK:
        limiter = _LIMITERS.get(name)
        if limiter is None:
            limiter = threading.BoundedSemaphore(max_workers)
            _LIMITERS[name] = limiter
        return limiter


@contextmanager
def parser_worker_limit(name: str, max_workers: int) -> Iterator[None]:
    """Limit concurrent access to heavy, process-wide parser operations.

    Set max_workers <= 0 to disable throttling for deployments that have enough
    CPU/GPU resources and know the parser backend is safe under concurrency.
    """

    if max_workers <= 0:
        yield
        return

    limiter = _get_limiter(name, max_workers)
    logger.debug("Waiting for %s parser slot (max_workers=%d)", name, max_workers)
    limiter.acquire()
    try:
        yield
    finally:
        limiter.release()
