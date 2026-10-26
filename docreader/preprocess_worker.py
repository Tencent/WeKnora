"""Private child-process entry point. Never run OCR in a gRPC worker thread."""

import io
import json
import logging
import os
import socket
import sys

from docreader.preprocess import WORKER_IDLE_SECONDS, PreprocessLimits, _Channel

logger = logging.getLogger(__name__)


def apply_process_limits(limits, parent_pid):
    if sys.platform.startswith("linux"):
        import ctypes
        import resource
        import signal

        # Kill the worker if the service is killed, including while native
        # inference is running. The parent-death race is checked afterwards.
        libc = ctypes.CDLL(None, use_errno=True)
        if libc.prctl(1, signal.SIGKILL, 0, 0, 0) != 0:  # PR_SET_PDEATHSIG
            raise OSError(ctypes.get_errno(), "Could not set parent-death signal")
        # The service can legitimately be PID 1 in a container. Compare with
        # the PID passed at spawn so a parent that died before prctl is caught.
        if os.getppid() != parent_pid:
            raise RuntimeError("Preprocessing parent exited")
        cap = limits.memory_mb * 1024 * 1024
        resource.setrlimit(resource.RLIMIT_AS, (cap, cap))


def process_image(image, limits):
    import cv2
    import numpy as np
    from PIL import Image, UnidentifiedImageError

    from docreader.parser.image_preprocess import preprocess, warp_ordered
    from docreader.proto.docreader_pb2 import PreprocessImageResponse

    try:
        # Inspect the header before allocating the full decoded image. The
        # encoded gRPC message size alone says nothing about pixel memory.
        try:
            with Image.open(io.BytesIO(image)) as header:
                width, height = header.size
                if width * height > limits.max_pixels:
                    return PreprocessImageResponse(fallback_needed=True, status="image_too_large")
        except (UnidentifiedImageError, OSError, Image.DecompressionBombError):
            return PreprocessImageResponse(fallback_needed=True, status="undecodable")

        bgr = cv2.imdecode(np.frombuffer(image, dtype=np.uint8), cv2.IMREAD_COLOR)
        if bgr is None:
            return PreprocessImageResponse(fallback_needed=True, status="undecodable")
        height, width = bgr.shape[:2]
        if width * height > limits.max_pixels:
            return PreprocessImageResponse(fallback_needed=True, status="image_too_large")

        # Detection only needs a probe: RapidOCR caps its own detector at 2000,
        # so a probe at that size loses no localisation. The crop, however, must
        # come from the ORIGINAL bytes — cropping the probe would scale every
        # glyph by the probe ratio (a 500px block would come back at 250px).
        probe = bgr
        probe_scale = min(1.0, limits.probe_edge / max(width, height))
        if probe_scale < 1:
            probe = cv2.resize(
                bgr,
                (max(1, round(width * probe_scale)), max(1, round(height * probe_scale))),
                interpolation=cv2.INTER_AREA,
            )
        out = preprocess(probe)
        fields = dict(
            theta=float(out.get("theta") or 0), residual=float(out.get("residual") or 0),
            n_boxes=int(out.get("n_boxes") or 0), n_kept=int(out.get("n_kept") or 0),
            flip=bool(out.get("flip")),
        )
        if out.get("status") != "ok" or out.get("rect") is None:
            return PreprocessImageResponse(
                fallback_needed=True, status=str(out.get("status", "unknown")), **fields)
        if fields["residual"] > 2:
            return PreprocessImageResponse(fallback_needed=True, status="low_quality", **fields)
        if probe is bgr:
            rect = out["rect"]  # the crop already came from the original
        else:
            # `quad_input` is the content quad in probe coordinates, already
            # ordered and padded; scale it back and crop the original image.
            factor = np.array([width / probe.shape[1], height / probe.shape[0]], "float32")
            quad = np.asarray(out["quad_input"], "float32").reshape(4, 2) * factor
            rect = warp_ordered(bgr, quad)
            if fields["flip"]:
                rect = cv2.rotate(rect, cv2.ROTATE_180)
        # The edge limit applies to the CROP, not to the source page: a small
        # text block keeps its pixels even when the page around it is huge.
        crop_scale = min(1.0, limits.max_edge / max(rect.shape[:2]))
        if crop_scale < 1:
            rect = cv2.resize(
                rect,
                (max(1, round(rect.shape[1] * crop_scale)),
                 max(1, round(rect.shape[0] * crop_scale))),
                interpolation=cv2.INTER_AREA,
            )
        ok, encoded = cv2.imencode(".jpg", rect, [cv2.IMWRITE_JPEG_QUALITY, 92])
        if not ok:
            return PreprocessImageResponse(fallback_needed=True, status="encode_failed", **fields)
        return PreprocessImageResponse(image=encoded.tobytes(), status="ok", **fields)
    except MemoryError:
        return PreprocessImageResponse(fallback_needed=True, status="memory_limit")
    except Exception:
        logger.exception("Preprocessing failed")
        return PreprocessImageResponse(fallback_needed=True, status="failed")


def main():
    limits = PreprocessLimits(**json.loads(sys.argv[2]))
    connection = _Channel(socket.socket(fileno=int(sys.argv[1])))
    try:
        apply_process_limits(limits, int(sys.argv[3]))
        import cv2
        from docreader.parser.image_preprocess import effective_thresholds

        cv2.setNumThreads(limits.threads)
        logging.basicConfig(level=logging.INFO)
        logger.info("Page straightening thresholds: %s", effective_thresholds())
        connection.send_bytes(b"ready")
        while connection.poll(WORKER_IDLE_SECONDS):
            image = connection.recv_bytes()
            response = process_image(image, limits)
            connection.send_bytes(response.SerializeToString())
            if response.status in ("memory_limit", "failed"):
                break
    except (EOFError, BrokenPipeError):
        pass
    finally:
        connection.close()


if __name__ == "__main__":
    main()
