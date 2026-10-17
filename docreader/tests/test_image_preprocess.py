"""Unit tests for the page-straightening geometry in parser/image_preprocess.

Every function tested here is pure: it takes boxes or points and returns numbers,
so the tests build their own inputs and never load RapidOCR. The three functions
that need the engine (detect, rec_score, preprocess) are exercised end to end
instead — see the R22 handover document and the smoke scripts under works/.

The values that matter most are the ones a wrong answer would hide:

  * dominant_angle must fold the 180-degree period, because a near-vertical page
    lands at BOTH ends of the (-90, 90] window and a median would call it 0;
  * filter_items must keep the box that sits exactly on a threshold, so a change
    from <= to < is visible rather than a silent one-box difference;
  * fit_quad must enclose every box it was given — a quad that cuts a line off
    is the failure the padding and the supporting lines exist to prevent.
"""
import unittest

import cv2
import numpy as np

from docreader.parser.image_preprocess import (
    ANG_TOL,
    DEFAULT_PAD,
    MIN_SCORE,
    PAD,
    QUAD_TOL,
    SIZE_MIN,
    _THRESHOLDS as THRESHOLDS,
    _boundary_line,
    _intersect,
    _tls,
    align_residual,
    box_angle,
    choose_rotation,
    dominant_angle,
    effective_thresholds,
    filter_items,
    fit_quad,
    order_pts,
    resolve_threshold,
    warp,
    wrap90,
)


def box(cx, cy, w, h, angle_deg=0.0):
    """One text-line box: four corners, long edge first, as RapidOCR returns them."""
    t = np.radians(angle_deg)
    ux, uy = np.cos(t), np.sin(t)
    vx, vy = -np.sin(t), np.cos(t)
    corners = [
        (cx - w / 2 * ux - h / 2 * vx, cy - w / 2 * uy - h / 2 * vy),
        (cx + w / 2 * ux - h / 2 * vx, cy + w / 2 * uy - h / 2 * vy),
        (cx + w / 2 * ux + h / 2 * vx, cy + w / 2 * uy + h / 2 * vy),
        (cx - w / 2 * ux + h / 2 * vx, cy - w / 2 * uy + h / 2 * vy),
    ]
    return np.array(corners, "float32")


def line(cx, cy, length, angle_deg=0.0, height=8.0, score=0.9):
    """A detected line: the box plus the score detect() would have attached."""
    return (box(cx, cy, length, height, angle_deg), score)


def grid(xs, ys):
    """The point set a box list turns into after the alignment transform."""
    return np.array([(float(x), float(y)) for x in xs for y in ys], "float32")


class BoxAngleTest(unittest.TestCase):
    def test_long_edge_sets_the_angle(self):
        angle, length = box_angle(box(50, 50, 100, 10, 30))
        self.assertAlmostEqual(angle, 30.0, places=3)
        self.assertAlmostEqual(length, 100.0, places=3)

    def test_a_tall_box_reports_its_long_edge_not_its_first(self):
        # w < h: the first edge is the short one, so the long edge decides.
        angle, length = box_angle(box(50, 50, 10, 100, 0))
        self.assertAlmostEqual(abs(angle), 90.0, places=3)
        self.assertAlmostEqual(length, 100.0, places=3)

    def test_a_vertical_box_reports_the_sign_of_its_long_edge(self):
        positive, _ = box_angle(box(50, 50, 100, 10, 45))
        negative, _ = box_angle(box(50, 50, 100, 10, -45))
        self.assertGreater(positive, 0)
        self.assertLess(negative, 0)


class Wrap90Test(unittest.TestCase):
    def test_folds_onto_the_period_a_line_really_has(self):
        for given, want in (
            (0.0, 0.0),
            (89.0, 89.0),
            (-89.0, -89.0),
            (90.0, -90.0),
            (-90.0, -90.0),
            (91.0, -89.0),
            (180.0, 0.0),
            (270.0, -90.0),
        ):
            with self.subTest(angle=given):
                self.assertAlmostEqual(wrap90(given), want, places=6)


class DominantAngleTest(unittest.TestCase):
    def test_no_boxes_is_no_angle(self):
        self.assertEqual(dominant_angle([]), 0.0)

    def test_a_flat_page_reads_zero(self):
        items = [line(10 * i, 50, 100, 0) for i in range(1, 6)]
        self.assertAlmostEqual(dominant_angle(items), 0.0, places=1)

    def test_a_tilted_page_reads_its_tilt(self):
        items = [line(10 * i, 50, 100, 30) for i in range(1, 6)]
        self.assertAlmostEqual(dominant_angle(items), 30.0, places=1)

    def test_a_quarter_turned_page_is_not_called_flat(self):
        # The load-bearing test of this file. A near-vertical page lands at BOTH
        # ends of the (-90, 90] window, because the sign of the long edge's x
        # component is pixel noise: the measured case split ~47% at -89 and ~37%
        # at +89. Here the short boxes near 0 are numerous enough to BE the
        # median of the raw angles, so an implementation that took one would
        # answer 0 and "align" the page by nothing; the length-weighted mode
        # answers the vertical direction the page actually has.
        items = [line(10 * i, 300, 10, 0) for i in range(1, 21)]
        items += [line(10 * i, 50, 100, -89) for i in range(1, 5)]
        items += [line(10 * i, 200, 100, 89) for i in range(1, 5)]
        angle = dominant_angle(items)
        self.assertAlmostEqual(abs(angle), 90.0, delta=0.5)

    def test_too_few_boxes_leave_the_angle_approximate(self):
        # Below three in-tolerance boxes the circular mean is not worth
        # trusting, so the answer is the window's own peak — the edge of a
        # plateau that can sit up to ANG_TOL away from the true angle. Two boxes
        # is not a page, so the bound is what this pins.
        items = [line(10, 50, 100, 20), line(200, 50, 100, 20)]
        self.assertAlmostEqual(dominant_angle(items), 20.0, delta=ANG_TOL)


class FilterItemsTest(unittest.TestCase):
    def test_keeps_what_agrees_and_drops_the_rest(self):
        items = [line(10 * i, 50, 100, 0) for i in range(1, 4)]
        items.append(line(500, 50, 100, 45))     # a background object's edge
        items.append(line(600, 50, 10, 0))       # a speck, far below the median
        kept = filter_items(items, 0.0)
        self.assertEqual(len(kept), 3)

    def test_a_box_just_inside_the_angle_threshold_is_kept(self):
        # Just inside and just outside rather than exactly on it: the angle
        # comes back through float32, so "exactly on it" is not a value the
        # arithmetic can be asked to produce. This still pins the threshold and
        # the direction of the comparison.
        items = [line(10 * i, 50, 100, 0) for i in range(1, 4)]
        items.append(line(500, 50, 100, ANG_TOL - 0.01))
        self.assertEqual(len(filter_items(items, 0.0)), 4)
        items[-1] = line(500, 50, 100, ANG_TOL + 0.01)
        self.assertEqual(len(filter_items(items, 0.0)), 3)

    def test_a_box_exactly_on_the_size_threshold_is_kept(self):
        items = [line(10 * i, 50, 100, 0) for i in range(1, 4)]
        items.append(line(500, 50, SIZE_MIN * 100, 0))
        self.assertEqual(len(filter_items(items, 0.0)), 4)
        items[-1] = line(500, 50, SIZE_MIN * 100 - 0.5, 0)
        self.assertEqual(len(filter_items(items, 0.0)), 3)

    def test_no_boxes_filters_to_nothing(self):
        self.assertEqual(filter_items([], 0.0), [])


class RotationTest(unittest.TestCase):
    def test_residual_is_smallest_at_the_alignment_that_matches(self):
        items = [line(10 * i, 50, 100, 30) for i in range(1, 6)]
        self.assertAlmostEqual(align_residual(items, 30), 0.0, places=1)
        self.assertAlmostEqual(align_residual(items, -30), 60.0, places=1)

    def test_rotation_is_chosen_by_the_smaller_residual(self):
        tilted_up = [line(10 * i, 50, 100, 30) for i in range(1, 6)]
        tilted_down = [line(10 * i, 50, 100, -30) for i in range(1, 6)]
        self.assertAlmostEqual(choose_rotation(tilted_up, 30), 30.0, places=6)
        self.assertAlmostEqual(choose_rotation(tilted_down, -30), -30.0, places=6)


class LeastSquaresTest(unittest.TestCase):
    def test_fits_a_line_through_points_on_one(self):
        pts = np.array([(x, 2.0 * x + 1.0) for x in range(10)], "float32")
        slope, intercept = _tls(pts, 0)
        self.assertAlmostEqual(slope, 2.0, places=3)
        self.assertAlmostEqual(intercept, 1.0, places=3)

    def test_needs_two_points_across_the_axis(self):
        self.assertIsNone(_tls(np.array([(5.0, 5.0)], "float32"), 0))
        self.assertIsNone(_tls(np.array([(5.0, 1.0), (5.0, 9.0)], "float32"), 0))


class BoundaryLineTest(unittest.TestCase):
    def test_the_min_side_encloses_every_point(self):
        pts = grid((0, 50, 100), (0, 40, 80))
        slope, intercept = _boundary_line(pts, 1, "min")
        residual = pts[:, 1] - (slope * pts[:, 0] + intercept)
        self.assertGreaterEqual(float(residual.min()), -1e-4)
        self.assertAlmostEqual(float(residual.min()), 0.0, places=3)

    def test_the_max_side_encloses_every_point(self):
        pts = grid((0, 50, 100), (0, 40, 80))
        slope, intercept = _boundary_line(pts, 1, "max")
        residual = pts[:, 1] - (slope * pts[:, 0] + intercept)
        self.assertLessEqual(float(residual.max()), 1e-4)
        self.assertAlmostEqual(float(residual.max()), 0.0, places=3)

    def test_a_flat_band_has_no_line(self):
        # Every point shares the coordinate this side is measured along, so
        # there is no outer edge to find.
        pts = np.array([(0.0, 5.0), (10.0, 5.0), (20.0, 5.0)], "float32")
        self.assertIsNone(_boundary_line(pts, 0, "min"))


class IntersectTest(unittest.TestCase):
    def test_two_lines_meet_where_they_should(self):
        # y = 0*x + 1  and  x = 0*y + 2  meet at (2, 1).
        self.assertEqual(_intersect((0.0, 1.0), (0.0, 2.0)), (2.0, 1.0))

    def test_parallel_lines_do_not_meet(self):
        # m1 * m2 == 1: the two lines are parallel, so the intersection is at
        # infinity and there is no corner.
        self.assertIsNone(_intersect((2.0, 1.0), (0.5, 3.0)))


class OrderPointsTest(unittest.TestCase):
    def test_corners_come_back_in_a_known_order(self):
        corners = np.array([(100.0, 80.0), (0.0, 0.0), (0.0, 80.0), (100.0, 0.0)], "float32")
        ordered = order_pts(corners)
        self.assertEqual([tuple(p) for p in ordered], [(0.0, 0.0), (100.0, 0.0), (100.0, 80.0), (0.0, 80.0)])


class FitQuadTest(unittest.TestCase):
    def test_the_quad_encloses_every_point_it_was_given(self):
        pts = grid(range(0, 101, 10), range(0, 81, 10))
        quad = fit_quad(pts)
        self.assertIsNotNone(quad)
        for x, y in pts:
            # pointPolygonTest returns the distance to the nearest edge, signed:
            # negative means the point is outside.
            distance = cv2.pointPolygonTest(quad, (float(x), float(y)), True)
            self.assertGreaterEqual(distance, -1e-3, f"point ({x}, {y}) fell outside the quad")

    def test_the_quad_is_tight_around_the_points(self):
        pts = grid(range(0, 101, 10), range(0, 81, 10))
        quad = fit_quad(pts)
        self.assertAlmostEqual(float(quad[:, 0].min()), 0.0, delta=1.0)
        self.assertAlmostEqual(float(quad[:, 0].max()), 100.0, delta=1.0)
        self.assertAlmostEqual(float(quad[:, 1].min()), 0.0, delta=1.0)
        self.assertAlmostEqual(float(quad[:, 1].max()), 80.0, delta=1.0)

    def test_points_on_one_line_have_no_quad(self):
        # Nothing spans the other axis, so two of the four supporting lines do
        # not exist and there is no content area to speak of.
        pts = np.array([(float(x), 5.0) for x in range(0, 101, 10)], "float32")
        self.assertIsNone(fit_quad(pts))


class WarpTest(unittest.TestCase):
    def test_the_result_is_the_quad_plus_its_padding(self):
        img = np.full((300, 300, 3), 255, "uint8")
        quad = np.array([(50.0, 50.0), (150.0, 50.0), (150.0, 100.0), (50.0, 100.0)], "float32")
        rect, padded = warp(img, quad)
        side = float(np.mean([np.linalg.norm(padded[(i + 1) % 4] - padded[i]) for i in range(4)]))
        grown = 2 * PAD * side
        self.assertAlmostEqual(rect.shape[1], 100 + grown, delta=2.0)
        self.assertAlmostEqual(rect.shape[0], 50 + grown, delta=2.0)

    def test_the_padding_grows_the_quad_outward(self):
        img = np.full((300, 300, 3), 255, "uint8")
        quad = np.array([(50.0, 50.0), (150.0, 50.0), (150.0, 100.0), (50.0, 100.0)], "float32")
        _, padded = warp(img, quad)
        self.assertLess(float(padded[:, 0].min()), 50.0)
        self.assertGreater(float(padded[:, 1].max()), 100.0)


class ThresholdsTest(unittest.TestCase):
    """The thresholds are env-overridable, so the override has to be trustworthy.

    The defaults are the tuned values; an override has to parse as a number in
    the threshold's range, and anything else is refused with the default left in
    place rather than clamped into range — a typo must not quietly run the
    pipeline at a value nobody chose.
    """

    def test_an_unset_override_leaves_the_tuned_default(self):
        for name, (default, _, _) in THRESHOLDS.items():
            with self.subTest(threshold=name):
                self.assertEqual(resolve_threshold(name, None), default)
                self.assertEqual(resolve_threshold(name, "   "), default)

    def test_a_valid_override_is_taken(self):
        self.assertEqual(resolve_threshold("ANG_TOL", "25"), 25.0)
        self.assertEqual(resolve_threshold("QUAD_TOL", "-0.05"), -0.05)
        self.assertEqual(resolve_threshold("PAD", "0.02"), 0.02)

    def test_the_ends_of_the_range_are_inside_it(self):
        for name, (_, low, high) in THRESHOLDS.items():
            with self.subTest(threshold=name):
                self.assertEqual(resolve_threshold(name, repr(low)), low)
                self.assertEqual(resolve_threshold(name, repr(high)), high)

    def test_a_value_outside_the_range_is_refused(self):
        for name, (default, low, high) in THRESHOLDS.items():
            for bad in (low - 1.0, high + 1.0):
                with self.subTest(threshold=name, value=bad):
                    self.assertEqual(resolve_threshold(name, repr(bad)), default)

    def test_a_value_that_is_not_a_number_is_refused(self):
        for raw in ("wide", "0.015x", "1,5", "nan-ish"):
            with self.subTest(raw=raw):
                self.assertEqual(resolve_threshold("PAD", raw), DEFAULT_PAD)

    def test_the_effective_set_is_what_the_module_uses(self):
        # The log line printed at import reads this, so it has to agree with the
        # names the functions actually close over.
        effective = effective_thresholds()
        self.assertEqual(effective["PAD"], PAD)
        self.assertEqual(effective["MIN_SCORE"], MIN_SCORE)
        self.assertEqual(effective["ANG_TOL"], ANG_TOL)
        self.assertEqual(effective["SIZE_MIN"], SIZE_MIN)
        self.assertEqual(effective["QUAD_TOL"], QUAD_TOL)


if __name__ == "__main__":
    unittest.main()
