// Copyright (c) OpenAI. All rights reserved.
"use strict";

function inferElementType(obj) {
  if (!obj) return "unknown";
  const data = obj.data || obj.options || {};
  // Distinguish lines explicitly via type only. Many objects have a 'line' style; don't misclassify those.
  if (obj.type === "line") return "line";
  if (obj.type && typeof obj.type === "string") return obj.type;
  if (obj.text || typeof data.text === "string") return "text";
  if (data.path || obj.image) return "image";
  if (data.chartType) return "chart";
  if (data.shape || data.line) return "shape";
  if (data.mediaType) return "media";
  if (data.table || Array.isArray(data.rows)) return "table";
  if (data.smartArt) return "smartart";
  return "unknown";
}

const TEXT_OVERLAP_ERROR_THRESHOLD = 0.1;
const RECTIFY_DIRECTION_EQUALITY_TOLERANCE = 0.15;

function warnIfSlideHasOverlaps(slide, pptx, options = {}) {
  if (!slide || !Array.isArray(slide._slideObjects)) {
    console.warn("Invalid slide object passed to warnIfSlideOverlaps()");
    return;
  }
  const opts = {
    // By default, containment cases are very common (e.g., full-slide backgrounds)
    // and usually not actionable. Mute them unless explicitly requested.
    muteContainment:
      options.muteContainment !== undefined ? options.muteContainment : true,
    // Do NOT ignore lines or decorative shapes by default; users want true overlaps.
    ignoreLines:
      options.ignoreLines !== undefined ? options.ignoreLines : false,
    ignoreDecorativeShapes:
      options.ignoreDecorativeShapes !== undefined
        ? options.ignoreDecorativeShapes
        : false,
  };
  const slideIndex =
    pptx && Array.isArray(pptx._slides) ? pptx._slides.indexOf(slide) : -1;
  const slideLabel =
    slideIndex >= 0 ? `Slide ${slideIndex + 1}` : "(Unknown slide index)";
  const formatElement = (el) => {
    const cx = (el.x + el.w / 2).toFixed(3);
    const cy = (el.y + el.h / 2).toFixed(3);
    return `element ${el.index} (${el.type}, center_x=${cx}, center_y=${cy})`;
  };
  const elements = slide._slideObjects.map((obj, i) => {
    const {
      x = 0,
      y = 0,
      w = 0,
      h = 0,
      fill,
      line,
    } = obj.data || obj.options || {};
    const type = inferElementType(obj);
    const isDecorative = (() => {
      if (!opts.ignoreDecorativeShapes) return false;
      // Border rectangles used as frames: transparent fill (or fully transparent) with a stroke
      const transparency =
        typeof fill?.transparency === "number" ? fill.transparency : null;
      const hasOnlyBorder = !!line && (!fill || transparency !== null);
      const fullyTransparent = transparency !== null && transparency >= 99;
      return type === "shape" && hasOnlyBorder && fullyTransparent;
    })();
    const ignorable = (opts.ignoreLines && type === "line") || isDecorative;
    return { index: i, type, x, y, w, h, ignorable };
  });
  let overlapCount = 0;
  let containmentCount = 0;
  for (let i = 0; i < elements.length; i++) {
    const a = elements[i];
    if (a.ignorable) continue;
    for (let j = i + 1; j < elements.length; j++) {
      const b = elements[j];
      if (b.ignorable) continue;
      const comparison = compareElementPosition(slide, a.index, b.index);
      if (comparison.relation === "overlapping") {
        // Special-case: diagonal line's bounding box overlapping a rectangle is often a false positive.
        const EPS = 1e-6;
        const getBounds = (e) => ({
          x: e.x,
          y: e.y,
          x2: e.x + e.w,
          y2: e.y + e.h,
        });
        const lineRectFalsePositive = (() => {
          const oneIsLine = (a.type === "line") ^ (b.type === "line");
          if (!oneIsLine) return false;
          const line = a.type === "line" ? a : b;
          const rect = a.type === "line" ? b : a;
          // If line is diagonal, verify actual segment intersects rect; if not, ignore.
          const isDiagonal = line.w > EPS && line.h > EPS;
          const lineSeg = {
            x1: line.x,
            y1: line.y,
            x2: line.x + line.w,
            y2: line.y + line.h,
          };
          const rectB = getBounds(rect);
          const pointInRect = (px, py, rb) =>
            px >= rb.x - EPS &&
            px <= rb.x2 + EPS &&
            py >= rb.y - EPS &&
            py <= rb.y2 + EPS;
          const segsIntersect = (p1, p2, q1, q2) => {
            const cross = (ax, ay, bx, by) => ax * by - ay * bx;
            const d1x = p2.x - p1.x,
              d1y = p2.y - p1.y;
            const d2x = q2.x - q1.x,
              d2y = q2.y - q1.y;
            const denom = cross(d1x, d1y, d2x, d2y);
            if (Math.abs(denom) < EPS) {
              // Parallel: check colinearity and overlapping projections
              const crossCol = cross(q1.x - p1.x, q1.y - p1.y, d1x, d1y);
              if (Math.abs(crossCol) > EPS) return false;
              const proj = (a, b, c) =>
                Math.min(Math.max(a, b), Math.max(Math.min(a, b), c));
              const overlapX = !(
                Math.max(p1.x, p2.x) < Math.min(q1.x, q2.x) - EPS ||
                Math.max(q1.x, q2.x) < Math.min(p1.x, p2.x) - EPS
              );
              const overlapY = !(
                Math.max(p1.y, p2.y) < Math.min(q1.y, q2.y) - EPS ||
                Math.max(q1.y, q2.y) < Math.min(p1.y, p2.y) - EPS
              );
              return overlapX && overlapY;
            }
            const t = cross(q1.x - p1.x, q1.y - p1.y, d2x, d2y) / denom;
            const u = cross(q1.x - p1.x, q1.y - p1.y, d1x, d1y) / denom;
            return t >= -EPS && t <= 1 + EPS && u >= -EPS && u <= 1 + EPS;
          };
          const intersectsRect = (seg, rb) => {
            if (
              pointInRect(seg.x1, seg.y1, rb) ||
              pointInRect(seg.x2, seg.y2, rb)
            )
              return true;
            const r1 = { x: rb.x, y: rb.y },
              r2 = { x: rb.x2, y: rb.y },
              r3 = { x: rb.x2, y: rb.y2 },
              r4 = { x: rb.x, y: rb.y2 };
            const p1 = { x: seg.x1, y: seg.y1 },
              p2 = { x: seg.x2, y: seg.y2 };
            return (
              segsIntersect(p1, p2, r1, r2) ||
              segsIntersect(p1, p2, r2, r3) ||
              segsIntersect(p1, p2, r3, r4) ||
              segsIntersect(p1, p2, r4, r1)
            );
          };
          return isDiagonal && !intersectsRect(lineSeg, rectB);
        })();
        if (!lineRectFalsePositive) {
          overlapCount++;

          const severeTextOverlap = (() => {
            if (!comparison.intersection) return false;
            const exceedsThreshold = (element) =>
              element.type === "text" &&
              comparison.intersection.w >= TEXT_OVERLAP_ERROR_THRESHOLD &&
              comparison.intersection.h >= TEXT_OVERLAP_ERROR_THRESHOLD;
            return exceedsThreshold(a) || exceedsThreshold(b);
          })();
          if (severeTextOverlap) {
            const overlapW = comparison.intersection.w;
            const overlapH = comparison.intersection.h;
            let rectificationSuggestion = "";
            if (overlapW > EPS && overlapH > EPS) {
              const maxOverlap = Math.max(overlapW, overlapH);
              const diffRatio = Math.abs(overlapW - overlapH) / maxOverlap;
              const directions = [];
              // Attempt to determine the primary direction of the overlap. This is the direction
              // in which the overlap is smaller (and so requires the smallest adjustment to rectify).
              if (diffRatio <= RECTIFY_DIRECTION_EQUALITY_TOLERANCE) {
                directions.push("horizontally", "vertically");
              } else if (overlapW < overlapH) {
                directions.push("horizontally");
              } else {
                directions.push("vertically");
              }
              rectificationSuggestion = `Suggestion: reposition elements ${directions.join(
                " and "
              )}.`;
            }

            console.error(
              `❌ ${slideLabel}: Severe text overlap detected between ${formatElement(
                a
              )} and ${formatElement(
                b
              )} (overlap_horizontal=${comparison.intersection.w.toFixed(
                3
              )}, overlap_vertical=${comparison.intersection.h.toFixed(
                3
              )}). THIS MUST BE FIXED. ${rectificationSuggestion}`
            );
          } else {
            console.warn(
              `⚠️ ${slideLabel}: Overlap detected between ${formatElement(
                a
              )} and ${formatElement(b)}.`
            );
          }
        }
      } else if (comparison.relation === "contained") {
        if (!opts.muteContainment) {
          containmentCount++;
          const container = elements[comparison.containerIndex];
          const contained = elements[comparison.containedIndex];
          console.warn(
            `⚠️ ${slideLabel}: ${formatElement(
              contained
            )} is fully contained within ${formatElement(container)}`
          );
        } else {
          // Still count internally when muted? We keep for summary only when un-muted
        }
      }
    }
  }
  if (!(overlapCount === 0 && (!containmentCount || opts.muteContainment))) {
    const issues = [];
    if (overlapCount > 0) issues.push(`${overlapCount} overlapping pair(s)`);
    if (!opts.muteContainment && containmentCount > 0)
      issues.push(`${containmentCount} containment case(s)`);
    console.log(`⚠️ ${slideLabel}: Found ${issues.join(" and ")}.`);
  }
}

function compareElementPosition(slide, firstIndex, secondIndex) {
  if (!slide || !Array.isArray(slide._slideObjects)) {
    throw new Error("Invalid slide object passed to compareElementPosition()");
  }
  if (
    typeof firstIndex !== "number" ||
    typeof secondIndex !== "number" ||
    !Number.isInteger(firstIndex) ||
    !Number.isInteger(secondIndex)
  ) {
    throw new Error("Element indices must be integer values.");
  }
  const elements = slide._slideObjects;
  if (
    firstIndex < 0 ||
    firstIndex >= elements.length ||
    secondIndex < 0 ||
    secondIndex >= elements.length
  ) {
    throw new Error(
      "Element index out of bounds for compareElementPosition()."
    );
  }
  const EPS = 1e-4;
  const getBounds = (obj) => {
    const source = obj?.data || obj?.options || {};
    let x = typeof source.x === "number" ? source.x : 0;
    let y = typeof source.y === "number" ? source.y : 0;
    let w = typeof source.w === "number" ? source.w : 0;
    let h = typeof source.h === "number" ? source.h : 0;
    if (source.sizing && source.sizing.type === "crop") {
      if (typeof source.sizing.w === "number") w = source.sizing.w;
      if (typeof source.sizing.h === "number") h = source.sizing.h;
    }
    return { x, y, w, h, x2: x + w, y2: y + h };
  };
  const boundsA = getBounds(elements[firstIndex]);
  const boundsB = getBounds(elements[secondIndex]);
  const separated =
    boundsA.x2 < boundsB.x - EPS ||
    boundsB.x2 < boundsA.x - EPS ||
    boundsA.y2 < boundsB.y - EPS ||
    boundsB.y2 < boundsA.y - EPS;
  if (separated) {
    return {
      relation: "disjoint",
      containerIndex: null,
      containedIndex: null,
      aBounds: boundsA,
      bBounds: boundsB,
      intersection: null,
    };
  }
  const aContainsB =
    boundsA.x <= boundsB.x + EPS &&
    boundsA.y <= boundsB.y + EPS &&
    boundsA.x2 >= boundsB.x2 - EPS &&
    boundsA.y2 >= boundsB.y2 - EPS;
  const bContainsA =
    boundsB.x <= boundsA.x + EPS &&
    boundsB.y <= boundsA.y + EPS &&
    boundsB.x2 >= boundsA.x2 - EPS &&
    boundsB.y2 >= boundsA.y2 - EPS;
  const ix1 = Math.max(boundsA.x, boundsB.x);
  const iy1 = Math.max(boundsA.y, boundsB.y);
  const ix2 = Math.min(boundsA.x2, boundsB.x2);
  const iy2 = Math.min(boundsA.y2, boundsB.y2);
  const intersectionWidth = Math.max(0, ix2 - ix1);
  const intersectionHeight = Math.max(0, iy2 - iy1);
  const intersection =
    intersectionWidth > EPS && intersectionHeight > EPS
      ? { x: ix1, y: iy1, w: intersectionWidth, h: intersectionHeight }
      : null;
  if (aContainsB && !bContainsA) {
    return {
      relation: "contained",
      containerIndex: firstIndex,
      containedIndex: secondIndex,
      aBounds: boundsA,
      bBounds: boundsB,
      intersection,
    };
  }
  if (bContainsA && !aContainsB) {
    return {
      relation: "contained",
      containerIndex: secondIndex,
      containedIndex: firstIndex,
      aBounds: boundsA,
      bBounds: boundsB,
      intersection,
    };
  }
  if (intersection) {
    return {
      relation: "overlapping",
      containerIndex: null,
      containedIndex: null,
      aBounds: boundsA,
      bBounds: boundsB,
      intersection,
    };
  }
  return {
    relation: "touching",
    containerIndex: null,
    containedIndex: null,
    aBounds: boundsA,
    bBounds: boundsB,
    intersection: null,
  };
}

const VALID_ALIGNMENTS = new Set([
  "left",
  "right",
  "top",
  "bottom",
  "verticallyCenter",
  "horizontallyCenter",
]);

const getElementBounds = (obj) => {
  const source = obj?.data || obj?.options || {};
  let x = typeof source.x === "number" ? source.x : 0;
  let y = typeof source.y === "number" ? source.y : 0;
  let w = typeof source.w === "number" ? source.w : 0;
  let h = typeof source.h === "number" ? source.h : 0;
  // If an image is placed with crop sizing, pptxgenjs stores a larger virtual image w/h
  // and a viewport in source.sizing.{w,h}. For visual overlap purposes, use the viewport.
  if (source.sizing && source.sizing.type === "crop") {
    if (typeof source.sizing.w === "number") w = source.sizing.w;
    if (typeof source.sizing.h === "number") h = source.sizing.h;
  }
  return { x, y, w, h, x2: x + w, y2: y + h };
};

const setElementPosition = (obj, coords) => {
  const ensureTarget = (targetObj) => {
    if (!targetObj || typeof targetObj !== "object") return null;
    return targetObj;
  };
  const targets = [];
  const dataTarget = ensureTarget(obj.data);
  if (dataTarget) targets.push(dataTarget);
  const optionsTarget =
    obj.options && obj.options !== obj.data ? ensureTarget(obj.options) : null;
  if (optionsTarget) targets.push(optionsTarget);
  if (targets.length === 0) {
    obj.data = obj.data && typeof obj.data === "object" ? obj.data : {};
    targets.push(obj.data);
  }
  targets.forEach((target) => {
    if (coords.x !== undefined) target.x = coords.x;
    if (coords.y !== undefined) target.y = coords.y;
  });
};

const dimensionKeyPairs = [
  ["width", "height"],
  ["w", "h"],
  ["cx", "cy"],
  ["slideWidth", "slideHeight"],
  ["slideWidthInches", "slideHeightInches"],
  ["widthInches", "heightInches"],
];

const toNumber = (value) => {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string") {
    const parsed = parseFloat(value);
    return Number.isFinite(parsed) ? parsed : null;
  }
  return null;
};

const readDimensionsFromObject = (candidate, seen = new Set()) => {
  if (!candidate || typeof candidate !== "object") return null;
  if (seen.has(candidate)) return null;
  seen.add(candidate);
  for (const [wKey, hKey] of dimensionKeyPairs) {
    const width = toNumber(candidate[wKey]);
    const height = toNumber(candidate[hKey]);
    if (width !== null && height !== null && width > 0 && height > 0) {
      return { width, height };
    }
  }
  const nestedKeys = ["size", "slideSize", "layout", "slideLayout"];
  for (const key of nestedKeys) {
    const nested = readDimensionsFromObject(candidate[key], seen);
    if (nested) return nested;
  }
  return null;
};

const getSlideDimensions = (slide, pptx) => {
  const candidates = [
    slide?._presLayout,
    slide?._slideLayout,
    slide?._pres?.layout,
    slide?._parent?.layout,
    slide?._layout,
    pptx?._presLayout,
    pptx?._layout,
    pptx?.layout,
    pptx?.presLayout,
  ];
  for (const candidate of candidates) {
    const dims = readDimensionsFromObject(candidate);
    if (dims) {
      // Some internals are in EMUs; convert if values look too large for inches
      const EMU_PER_IN = 914400;
      const looksEmu = dims.width > 1000 || dims.height > 1000;
      if (looksEmu) {
        return {
          width: dims.width / EMU_PER_IN,
          height: dims.height / EMU_PER_IN,
          source: "emu_converted",
        };
      }
      return { ...dims, source: "detected" };
    }
  }
  throw new Error(
    "getSlideDimensions(): Unable to determine slide dimensions from pptxgenjs internals."
  );
};

function alignSlideElements(slide, indices, alignment) {
  if (!slide || !Array.isArray(slide._slideObjects)) {
    throw new Error("Invalid slide object passed to alignSlideElements()");
  }
  if (!Array.isArray(indices) || indices.length === 0) {
    throw new Error("indices must be a non-empty array.");
  }
  if (!VALID_ALIGNMENTS.has(alignment)) {
    throw new Error(`Unsupported alignment option: ${alignment}`);
  }
  const uniqueIndices = [...new Set(indices)];
  const elements = slide._slideObjects;
  const selected = uniqueIndices.map((idx) => {
    if (typeof idx !== "number" || !Number.isInteger(idx)) {
      throw new Error("Element indices must be integers.");
    }
    if (idx < 0 || idx >= elements.length) {
      throw new Error("Element index out of bounds for alignSlideElements().");
    }
    const obj = elements[idx];
    const bounds = getElementBounds(obj);
    return { index: idx, obj, bounds };
  });
  if (selected.length < 2) return;
  const minX = Math.min(...selected.map((item) => item.bounds.x));
  const maxX2 = Math.max(...selected.map((item) => item.bounds.x2));
  const minY = Math.min(...selected.map((item) => item.bounds.y));
  const maxY2 = Math.max(...selected.map((item) => item.bounds.y2));
  const centerX = (minX + maxX2) / 2;
  const centerY = (minY + maxY2) / 2;
  selected.forEach(({ obj, bounds }) => {
    const { w, h } = bounds;
    switch (alignment) {
      case "left":
        setElementPosition(obj, { x: minX });
        break;
      case "right":
        setElementPosition(obj, { x: maxX2 - w });
        break;
      case "top":
        setElementPosition(obj, { y: minY });
        break;
      case "bottom":
        setElementPosition(obj, { y: maxY2 - h });
        break;
      case "horizontallyCenter":
        setElementPosition(obj, { x: centerX - w / 2 });
        break;
      case "verticallyCenter":
        setElementPosition(obj, { y: centerY - h / 2 });
        break;
      default:
        throw new Error(`Unhandled alignment option: ${alignment}`);
    }
  });
}

function distributeSlideElements(slide, indices, direction) {
  if (!slide || !Array.isArray(slide._slideObjects)) {
    throw new Error("Invalid slide object passed to distributeSlideElements()");
  }
  if (!Array.isArray(indices) || indices.length === 0) {
    throw new Error("indices must be a non-empty array.");
  }
  if (direction !== "horizontal" && direction !== "vertical") {
    throw new Error(`Unsupported distribution direction: ${direction}`);
  }
  const uniqueIndices = [...new Set(indices)];
  if (uniqueIndices.length < 2) return;
  const elements = slide._slideObjects;
  const selected = uniqueIndices.map((idx) => {
    if (typeof idx !== "number" || !Number.isInteger(idx)) {
      throw new Error("Element indices must be integers.");
    }
    if (idx < 0 || idx >= elements.length) {
      throw new Error(
        "Element index out of bounds for distributeSlideElements()."
      );
    }
    const obj = elements[idx];
    const bounds = getElementBounds(obj);
    return { index: idx, obj, bounds };
  });
  const axisStartKey = direction === "horizontal" ? "x" : "y";
  const axisEndKey = direction === "horizontal" ? "x2" : "y2";
  const sizeKey = direction === "horizontal" ? "w" : "h";
  selected.sort((a, b) => {
    const delta = a.bounds[axisStartKey] - b.bounds[axisStartKey];
    return Math.abs(delta) > 1e-6 ? delta : a.index - b.index;
  });
  const minCoord = Math.min(
    ...selected.map((item) => item.bounds[axisStartKey])
  );
  const maxCoord = Math.max(...selected.map((item) => item.bounds[axisEndKey]));
  const totalSpan = maxCoord - minCoord;
  const gaps = selected.length - 1;
  const totalSize = selected.reduce(
    (sum, item) => sum + item.bounds[sizeKey],
    0
  );
  const gapSize = gaps > 0 ? (totalSpan - totalSize) / gaps : 0;
  let cursor = minCoord;
  selected.forEach(({ obj, bounds }) => {
    if (direction === "horizontal") {
      setElementPosition(obj, { x: cursor });
      cursor += bounds.w + gapSize;
    } else {
      setElementPosition(obj, { y: cursor });
      cursor += bounds.h + gapSize;
    }
  });
}

function warnIfSlideElementsOutOfBounds(slide, pptx) {
  if (!slide || !Array.isArray(slide._slideObjects)) {
    console.warn(
      "Invalid slide object passed to warnIfSlideElementsOutOfBounds()"
    );
    return;
  }
  const {
    width: slideWidth,
    height: slideHeight,
    source,
  } = getSlideDimensions(slide, pptx);
  const slideIndex =
    pptx && Array.isArray(pptx._slides) ? pptx._slides.indexOf(slide) : -1;
  const slideLabel =
    slideIndex >= 0 ? `Slide ${slideIndex + 1}` : "(Unknown slide index)";
  if (source === "default") {
    console.warn(
      `⚠️ ${slideLabel}: Unable to determine slide dimensions from pptxgenjs internals; assuming width=${slideWidth}, height=${slideHeight}.`
    );
  }
  const EPS = 1e-4;
  let outOfBoundsCount = 0;
  const formatElement = (idx, type, bounds) => {
    const cx = (bounds.x + bounds.w / 2).toFixed(3);
    const cy = (bounds.y + bounds.h / 2).toFixed(3);
    return `Element ${idx} (${type}, center_x=${cx}, center_y=${cy})`;
  };
  slide._slideObjects.forEach((obj, index) => {
    const bounds = getElementBounds(obj);
    const type = inferElementType(obj);
    const violations = [];
    if (bounds.x < -EPS) violations.push(`left=${bounds.x.toFixed(3)} < 0`);
    if (bounds.y < -EPS) violations.push(`top=${bounds.y.toFixed(3)} < 0`);
    if (bounds.x2 > slideWidth + EPS)
      violations.push(
        `right=${bounds.x2.toFixed(3)} > width=${slideWidth.toFixed(3)}`
      );
    if (bounds.y2 > slideHeight + EPS)
      violations.push(
        `bottom=${bounds.y2.toFixed(3)} > height=${slideHeight.toFixed(3)}`
      );
    if (violations.length > 0) {
      outOfBoundsCount++;
      console.warn(
        `⚠️ ${slideLabel}: ${formatElement(
          index,
          type,
          bounds
        )} exceeds slide bounds (${violations.join(", ")}).`
      );
    }
  });
  if (outOfBoundsCount > 0) {
    console.log(
      `⚠️ ${slideLabel}: Found ${outOfBoundsCount} element(s) extending beyond the slide bounds.`
    );
  }
}

module.exports = {
  inferElementType,
  compareElementPosition,
  warnIfSlideHasOverlaps,
  alignSlideElements,
  distributeSlideElements,
  warnIfSlideElementsOutOfBounds,
  getSlideDimensions,
};
