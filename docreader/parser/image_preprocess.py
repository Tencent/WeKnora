#!/usr/bin/env python3
"""Straighten and crop a photographed page before OCR reads it.

The working assumption is that the union of RapidOCR's text-line boxes is the
page's content area, so a content-tight rectangle can be recovered from text
geometry alone — no page detector and no model beyond the recogniser already in
the image pipeline.

Per input image:

  1. RapidOCR detects the text-line boxes, each with a rotation angle;
  2. filter them: score, angle consensus (within ANG_TOL of the dominant angle,
     which is what kills detections on background objects) and size (long edge
     at least SIZE_MIN of the median);
  3. align by the dominant text direction (two candidate signs, chosen by the
     smaller residual; a +/-90deg page is kept, never collapsed to 0);
  4. fit the content quad inside the ALIGNED coordinate system (outer supporting
     lines through the extreme point sets, intersected), then pad it by PAD;
  5. warpPerspective inside that same coordinate system, giving a rectangular
     content image with horizontal text;
  6. decide 0 vs 180 degrees by comparing the mean recognition score of the
     result with that of its 180deg rotation (with cls disabled, so the score
     is orientation-sensitive);
  7. compare against a content-level reference crop (aspect ratio, then by eye).

Every failure mode is a verdict rather than an exception — the caller keeps its
own bytes — so a page that cannot be corrected is read as the original
photograph instead of something worse.
"""
import logging
import os
import threading

import cv2
import numpy as np
from rapidocr import RapidOCR

from docreader.config import CONFIG

logger = logging.getLogger(__name__)

# The thresholds, tuned on the 17-image benchmark (see the R22 handover
# document). Each one can be overridden for a debugging run through the
# environment — WEKNORA_PREPROCESS_<NAME> — because re-tuning a constant meant
# rebuilding the image, which is a long way round to answer "what happens if
# this page is scored a little differently".
#
# The default IS the tuned value; an override is validated against a range and
# a bad one is refused rather than clamped, so a typo cannot quietly run the
# pipeline at a value nobody chose. The set in force is named in the server's
# startup log, so a run's output can be read back against the tuning that
# produced it.
DEFAULT_PAD = 0.015
DEFAULT_MIN_SCORE = 0.5
DEFAULT_ANG_TOL = 15.0
DEFAULT_SIZE_MIN = 0.35
DEFAULT_QUAD_TOL = -0.02

_ENV_PREFIX = "WEKNORA_PREPROCESS_"

# name -> (default, low, high). The range is what the value has to mean
# something in, not a second tuning: PAD is a fraction of the page side,
# MIN_SCORE and SIZE_MIN are fractions, ANG_TOL is an angle in degrees, and
# QUAD_TOL is a negative allowance relative to the quad's longer side.
_THRESHOLDS = {
    "PAD": (DEFAULT_PAD, 0.0, 0.2),
    "MIN_SCORE": (DEFAULT_MIN_SCORE, 0.0, 1.0),
    "ANG_TOL": (DEFAULT_ANG_TOL, 0.0, 90.0),
    "SIZE_MIN": (DEFAULT_SIZE_MIN, 0.0, 1.0),
    "QUAD_TOL": (DEFAULT_QUAD_TOL, -0.5, 0.0),
}


def resolve_threshold(name: str, raw: "str | None") -> float:
    """The value one threshold takes for one raw environment string.

    Unset or blank means the tuned default. Anything else has to parse as a
    number inside the threshold's range, or the default is used and the reason
    is logged — a refused override is louder than a silent one.
    """
    default, low, high = _THRESHOLDS[name]
    if raw is None or raw.strip() == "":
        return default
    try:
        value = float(raw)
    except ValueError:
        logger.warning("%s%s=%r is not a number; using the default %g", _ENV_PREFIX, name, raw, default)
        return default
    if not low <= value <= high:
        logger.warning("%s%s=%g is outside [%g, %g]; using the default %g",
                       _ENV_PREFIX, name, value, low, high, default)
        return default
    return value


PAD = resolve_threshold("PAD", os.environ.get(_ENV_PREFIX + "PAD"))
MIN_SCORE = resolve_threshold("MIN_SCORE", os.environ.get(_ENV_PREFIX + "MIN_SCORE"))
ANG_TOL = resolve_threshold("ANG_TOL", os.environ.get(_ENV_PREFIX + "ANG_TOL"))
SIZE_MIN = resolve_threshold("SIZE_MIN", os.environ.get(_ENV_PREFIX + "SIZE_MIN"))
QUAD_TOL = resolve_threshold("QUAD_TOL", os.environ.get(_ENV_PREFIX + "QUAD_TOL"))


def effective_thresholds() -> dict:
    """The thresholds this process is running with, by name.

    The server logs this once at startup (see docreader.main), so the output of
    a debugging run can be read back against the tuning that produced it. It is
    not logged from here: this module is imported before the log level is set.
    """
    return {
        "PAD": PAD,
        "MIN_SCORE": MIN_SCORE,
        "ANG_TOL": ANG_TOL,
        "SIZE_MIN": SIZE_MIN,
        "QUAD_TOL": QUAD_TOL,
    }

# RapidOCR instances carry mutable state: parameters are written onto the
# instance (update_params -> setattr), so calling ocr(img, use_cls=...) changes
# what every later call sees. The gRPC server handles requests concurrently, so
# one instance is shared per thread rather than globally.
_ocr_local = threading.local()


def _engine() -> "RapidOCR":
    engine = getattr(_ocr_local, "engine", None)
    if engine is None:
        engine = RapidOCR(params={
            "EngineConfig.onnxruntime.intra_op_num_threads": CONFIG.preprocess_threads,
            "EngineConfig.onnxruntime.inter_op_num_threads": 1,
        })
        _ocr_local.engine = engine
    return engine


def box_angle(b):
    """(angle of a box's long edge in degrees, length of that edge)."""
    d1 = float(np.linalg.norm(b[1] - b[0]))
    d2 = float(np.linalg.norm(b[2] - b[1]))
    v = (b[1] - b[0]) if d1 >= d2 else (b[2] - b[1])
    return float(np.degrees(np.arctan2(v[1], v[0]))), max(d1, d2)


def wrap90(a):
    """Fold an angle into (-90, 90], the period a text line really has."""
    return ((a + 90.0) % 180.0) - 90.0


def detect(img):
    # use_cls MUST be passed explicitly: RapidOCR.__call__(use_cls=...) writes the
    # flag onto the instance (rapidocr.main.update_params -> setattr), so a later
    # rec_score(img, use_cls=False) would silently change the DETECTION result of
    # every subsequent call on the shared instance (measured on up180: 166 boxes
    # with cls on -> 147 with cls off, losing exactly the long text lines that
    # carry the dominant angle).
    res = _engine()(img, use_cls=True)
    return [(np.array(b, "float32"), float(s)) for b, s in zip(res.boxes, res.scores) if s >= MIN_SCORE]


def dominant_angle(items):
    """Length-weighted MODE of the text-line angle on the 180deg-periodic circle.

    Must not be a plain median: near-vertical boxes land at BOTH ends of the
    (-90, 90] window, because the sign of the long edge's x component is pixel
    noise. Measured on the 90deg set, a 90deg-rotated page split into 46.9% at
    -89 and 37.1% at +89 -> a median landed at 0.0 and the page was "aligned"
    by 0deg (wrong). Working on angle mod 180 with a tolerance-width window
    merges those two halves and returns ~90.
    """
    if not items:
        return 0.0
    ang = np.array([box_angle(b)[0] % 180.0 for b, _ in items])
    w = np.array([box_angle(b)[1] for b, _ in items])
    grid = np.arange(0.0, 180.0, 0.5)
    d = np.abs(((grid[:, None] - ang[None, :]) + 90.0) % 180.0 - 90.0)
    score = (w[None, :] * (d <= ANG_TOL)).sum(axis=1)
    peak = float(grid[int(np.argmax(score))])
    # The argmax sits at the EDGE of a plateau (any window covering the same
    # cluster scores identically), so refine: circular mean of the in-tolerance
    # boxes, weighted by length. Without this the output keeps a 5-15deg tilt.
    sel = np.abs(((peak - ang) + 90.0) % 180.0 - 90.0) <= ANG_TOL
    if sel.sum() < 3:
        return wrap90(peak)
    a2 = np.radians(2.0 * ang[sel])
    sw = w[sel]
    return wrap90(float(np.degrees(np.arctan2((sw * np.sin(a2)).sum(), (sw * np.cos(a2)).sum())) / 2.0))


def filter_items(items, theta):
    """Keep the boxes that agree with the dominant angle and are not too small.

    A false detection on a background object (a keyboard, a phone) fails on both
    counts: its angle is unrelated to the text and its box is short.
    """
    med = float(np.median([box_angle(b)[1] for b, _ in items])) if items else 0.0
    keep = []
    for b, s in items:
        a, L = box_angle(b)
        if abs(wrap90(a - theta)) <= ANG_TOL and L >= SIZE_MIN * med:
            keep.append((b, s))
    return keep


def rotate_expand(img, ang):
    """Rotate with an expanded canvas (PIL expand=True equivalent); return (img, M)."""
    h, w = img.shape[:2]
    M = cv2.getRotationMatrix2D((w / 2.0, h / 2.0), ang, 1.0)
    cos, sin = abs(M[0, 0]), abs(M[0, 1])
    nW, nH = int(h * sin + w * cos), int(h * cos + w * sin)
    M[0, 2] += nW / 2.0 - w / 2.0
    M[1, 2] += nH / 2.0 - h / 2.0
    return cv2.warpAffine(img, M, (nW, nH), borderValue=(255, 255, 255)), M


def align_residual(items, angle_deg):
    """Median |residual| of box angles after rotating by `angle_deg` (cv2 convention)."""
    c, s = np.cos(np.radians(angle_deg)), np.sin(np.radians(angle_deg))
    Rm = np.array([[c, s], [-s, c]], "float32")
    res = []
    for b, _ in items:
        d1 = float(np.linalg.norm(b[1] - b[0]))
        d2 = float(np.linalg.norm(b[2] - b[1]))
        v = (b[1] - b[0]) if d1 >= d2 else (b[2] - b[1])
        w = Rm @ v
        res.append(wrap90(float(np.degrees(np.arctan2(w[1], w[0])))))
    return float(np.median(np.abs(res)))


def choose_rotation(items, theta):
    """The sign of theta that leaves the smaller residual: -theta or theta.

    Both signs are real candidates because the dominant angle is only known
    modulo 180, and a page rotated by +90 and one rotated by -90 are different
    pictures even though their text lines share an angle.
    """
    return -theta if align_residual(items, -theta) <= align_residual(items, theta) else theta


def _tls(points, along):
    """Fit `other = m*along + b` by least squares (bands are narrow -> TLS~LSQ)."""
    x, y = points[:, along], points[:, 1 - along]
    if len(x) < 2 or x.max() - x.min() < 1e-6:
        return None
    m = float(np.polyfit(x, y, 1)[0])
    return (m, float(np.mean(y) - m * np.mean(x)))


def _boundary_line(pts, axis, side, frac=0.10, max_slope=0.12):
    """Outer SUPPORTING line for one side: robust slope + outward shift.

    Slope comes from the extreme band ONLY IF that band spans enough of the other
    axis (a small cluster extrapolated across the full width gives wild slopes).
    Otherwise the line is axis-aligned — legitimate here because the frame was
    already rotated so that text lines are horizontal / columns vertical.
    Then the line is shifted outward until it touches the outermost point, so the
    quad always ENCLOSES every detected box (the plain band fit cut 550px off).
    """
    other = pts[:, 1 - axis]
    span = float(np.ptp(other))
    if span < 1e-6:
        return None
    # Select the outer band across the boundary, then measure its coverage
    # along the boundary. Using the same axis for both makes the 50% coverage
    # requirement impossible for a 10% band.
    coord = pts[:, axis]
    lo, hi = float(coord.min()), float(coord.max())
    band = (hi - lo) * frac
    sel = pts[coord <= lo + band] if side == "min" else pts[coord >= hi - band]
    m = 0.0
    if len(sel) >= 5 and np.ptp(sel[:, 1 - axis]) >= 0.5 * span:
        m = float(np.polyfit(sel[:, 1 - axis], sel[:, axis], 1)[0])
        if abs(m) > max_slope:
            m = 0.0
    b0 = float(np.mean(sel[:, axis]) - m * np.mean(sel[:, 1 - axis]))
    r = pts[:, axis] - (m * pts[:, 1 - axis] + b0)
    b = b0 + (float(r.min()) if side == "min" else float(r.max()))
    return (m, b)          # coord_axis = m * coord_other + b


def _intersect(line_h, line_v):
    """line_h: y = m*x + b;  line_v: x = m*y + b  ->  x = (m2*b1 + b2)/(1 - m1*m2)."""
    (m1, b1), (m2, b2) = line_h, line_v
    den = 1.0 - m1 * m2
    if abs(den) < 1e-9:
        return None
    x = (m2 * b1 + b2) / den
    return (x, m1 * x + b1)


def order_pts(p):
    """Order four corners as top-left, top-right, bottom-right, bottom-left."""
    p = np.asarray(p, "float32").reshape(4, 2)
    s, d = p.sum(1), np.diff(p, axis=1).ravel()
    return np.array([p[np.argmin(s)], p[np.argmin(d)], p[np.argmax(s)], p[np.argmax(d)]], "float32")


def fit_quad(pts):
    """Content quad = intersections of four OUTER supporting lines of the boxes.

    The supporting lines enclose every point, so a first attempt can come back
    with a few outliers still inside the tolerance band; those are dropped and
    the quad is refitted, which is why the loop exists. `QUAD_TOL` is a negative
    allowance: a point is accepted while it is no further outside than that
    fraction of the quad's longer side.
    """
    quad = None
    for _ in range(3):
        top = _boundary_line(pts, 1, "min")      # y = m*x + b, above all points
        bot = _boundary_line(pts, 1, "max")      # y = m*x + b, below all points
        lef = _boundary_line(pts, 0, "min")      # x = m*y + b, left of all points
        rig = _boundary_line(pts, 0, "max")      # x = m*y + b, right of all points
        if None in (top, bot, lef, rig):
            return None
        corners = [_intersect(top, lef), _intersect(top, rig),
                   _intersect(bot, rig), _intersect(bot, lef)]
        if None in corners:
            return None
        quad = np.array(corners, "float32")
        xr = quad[:, 0].max() - quad[:, 0].min()
        yr = quad[:, 1].max() - quad[:, 1].min()
        tol = QUAD_TOL * max(xr, yr)
        inside = np.array([cv2.pointPolygonTest(quad, (float(p[0]), float(p[1])), True) >= tol
                           for p in pts])
        if inside.all():
            return quad
        pts = pts[inside]
        if len(pts) < 8:
            return quad
    return quad


def warp(img, quad, pad=PAD):
    """Perspective-warp the padded quad into a rectangle; return (rect, quad)."""
    q = order_pts(quad)
    side = float(np.mean([np.linalg.norm(q[(i + 1) % 4] - q[i]) for i in range(4)]))
    d = pad * side
    q = q + np.array([[-d, -d], [d, -d], [d, d], [-d, d]], "float32")
    wA, wB = np.linalg.norm(q[1] - q[0]), np.linalg.norm(q[2] - q[3])
    hA, hB = np.linalg.norm(q[3] - q[0]), np.linalg.norm(q[2] - q[1])
    Wt, Ht = int(max(wA, wB)), int(max(hA, hB))
    dst = np.array([[0, 0], [Wt - 1, 0], [Wt - 1, Ht - 1], [0, Ht - 1]], "float32")
    rect = cv2.warpPerspective(img, cv2.getPerspectiveTransform(q, dst), (Wt, Ht),
                               borderValue=(255, 255, 255))
    return rect, q


def warp_ordered(img, quad, pad=0.0):
    """Warp an ALREADY-ORDERED quad (TL, TR, BR, BL), with optional extra padding.

    `preprocess`'s `quad_input` is already ordered and padded in the rotated
    frame, so it must NOT go through `order_pts` again: on a rotated page the
    sum/difference heuristic can pick a different corner and the crop comes back
    twisted. This lets a caller detect on a smaller probe and still crop the
    original bytes at their own resolution.
    """
    q = np.asarray(quad, "float32").reshape(4, 2)
    if pad:
        side = float(np.mean([np.linalg.norm(q[(i + 1) % 4] - q[i]) for i in range(4)]))
        d = pad * side
        q = q + np.array([[-d, -d], [d, -d], [d, d], [-d, d]], "float32")
    wA, wB = np.linalg.norm(q[1] - q[0]), np.linalg.norm(q[2] - q[3])
    hA, hB = np.linalg.norm(q[3] - q[0]), np.linalg.norm(q[2] - q[1])
    Wt, Ht = int(max(wA, wB)), int(max(hA, hB))
    dst = np.array([[0, 0], [Wt - 1, 0], [Wt - 1, Ht - 1], [0, Ht - 1]], "float32")
    return cv2.warpPerspective(img, cv2.getPerspectiveTransform(q, dst), (Wt, Ht),
                               borderValue=(255, 255, 255))


def rec_score(img):
    """Mean recognition score with cls DISABLED (orientation-sensitive)."""
    try:
        res = _engine()(img, use_cls=False)
        sc = np.asarray(res.scores if res.scores is not None else [], "float32").ravel()
        return (float(sc.mean()) if sc.size else 0.0), int(sc.size)
    except Exception as e:
        logger.warning("Recognition score unavailable, treating the image as unreadable: %s", e)
        return 0.0, 0


def preprocess(bgr: "np.ndarray") -> dict:
    """Straighten and crop one photographed page (R22 end to end).

    Returns a dict: rect (corrected BGR image or None), quad_input (content quad
    in the INPUT image's coordinates), theta / ang / residual / flip / s0 / s180 /
    n_boxes / n_kept, and status = "ok" | "no_text" | "low_quality".

    A non-"ok" status is a verdict, not an exception: the caller keeps its own
    original bytes. Callers must not treat it as an error to log.
    """
    items = detect(bgr)
    if len(items) < 4:
        return {"rect": None, "status": "no_text", "n_boxes": len(items), "n_kept": 0}
    theta = dominant_angle(items)
    kept = filter_items(items, theta)
    if len(kept) < 4:
        return {"rect": None, "status": "no_text", "n_boxes": len(items), "n_kept": len(kept)}
    ang = choose_rotation(kept, theta)
    residual = align_residual(kept, ang)
    rot, M = rotate_expand(bgr, ang)
    pts = cv2.transform(np.concatenate([b for b, _ in kept]).reshape(-1, 1, 2), M).reshape(-1, 2)
    quad_r = fit_quad(pts)
    if quad_r is None:
        return {"rect": None, "status": "low_quality", "theta": float(theta), "ang": float(ang),
                "residual": float(residual), "n_boxes": len(items), "n_kept": len(kept)}
    rect, quad_pad = warp(rot, quad_r)
    s0, _ = rec_score(rect)
    s1, _ = rec_score(cv2.rotate(rect, cv2.ROTATE_180))
    flip = bool(s1 > s0)
    if flip:
        rect = cv2.rotate(rect, cv2.ROTATE_180)
    back = cv2.invertAffineTransform(M)
    quad_o = cv2.transform(quad_pad.reshape(-1, 1, 2), back).reshape(-1, 2)
    return {
        "rect": rect,
        "quad_input": quad_o.astype("float32"),
        "theta": float(theta),
        "ang": float(ang),
        "residual": float(residual),
        "flip": flip,
        "s0": float(s0),
        "s180": float(s1),
        "n_boxes": len(items),
        "n_kept": len(kept),
        "status": "ok",
    }
