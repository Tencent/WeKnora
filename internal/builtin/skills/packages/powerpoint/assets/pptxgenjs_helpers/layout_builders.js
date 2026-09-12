// Copyright (c) OpenAI. All rights reserved.
"use strict";

const { calcTextBox, autoFontSize } = require("./text");
const { imageSizingCrop, imageSizingContain } = require("./image");
const { getSlideDimensions } = require("./layout");

module.exports = {
  addImageTextCard,
  addCardRow,
  addThreeLevelTree,
};

function addImageTextCard(slide, opts = {}) {
  const x = toNumberOr(opts.x, 0);
  const y = toNumberOr(opts.y, 0);
  const w = toNumberOr(opts.width, 3.0);
  const gap = toNumberOr(opts.gap, 0.15);
  const image = opts.image || {};
  const text = opts.text || "";
  const textBox = opts.textBox || {};

  const boxH = toNumberOr(image.boxHeight, 2.2);
  const sizing = (image.sizing || "crop").toLowerCase();
  let imgPlacement;
  if (image.path || image.data) {
    const base = image.path ? { path: image.path } : { data: image.data };
    if (sizing === "contain") {
      imgPlacement = imageSizingContain(
        image.path || image.data,
        x,
        y,
        w,
        boxH
      );
      slide.addImage({ ...base, ...imgPlacement });
    } else {
      const c = image.crop || {};
      imgPlacement = imageSizingCrop(
        image.path || image.data,
        x,
        y,
        w,
        boxH,
        c.cx,
        c.cy,
        c.cw,
        c.ch
      );
      slide.addImage({ ...base, ...imgPlacement });
    }
  }

  const textY = y + boxH + gap;
  const fontSize = toNumberOr(textBox.fontSize, 14);
  const fontFaceRaw = textBox.fontFace;
  const fontFace =
    typeof fontFaceRaw === "string" && fontFaceRaw.trim().length > 0
      ? fontFaceRaw.trim()
      : null;
  if (!fontFace) {
    throw new Error(
      "addImageTextCard(): textBox.fontFace is required for text measurement."
    );
  }
  let hText;
  let textOptions;

  if (textBox.h != null && Number.isFinite(toNumberOr(textBox.h, NaN))) {
    // Layout-first: caller fixed the box height, so adjust font size to fit via autoFontSize.
    const fixedH = toNumberOr(textBox.h, 0);
    const baseOpts = {
      x,
      y: textY,
      w,
      h: fixedH,
      mode: textBox.mode || "auto",
      fontSize,
      minFontSize: textBox.minFontSize,
      maxFontSize: textBox.maxFontSize,
      margin: textBox.margin,
      paraSpaceAfter: textBox.paraSpaceAfter,
    };
    const autoOpts = autoFontSize(text, fontFace, baseOpts);
    hText = fixedH;
    textOptions = {
      ...autoOpts,
      fontFace,
      color: textBox.color,
      align: textBox.align,
      valign: textBox.valign || "top",
      fill: opts.background,
    };
  } else {
    // Content-first: fixed font size, let calcTextBox derive the required height.
    const layout = calcTextBox(fontSize, {
      text,
      w,
      fontFace,
      margin: textBox.margin,
      paraSpaceAfter: textBox.paraSpaceAfter,
    });
    hText = layout.h;
    textOptions = {
      x,
      y: textY,
      w,
      h: hText,
      fontFace,
      fontSize,
      color: textBox.color,
      align: textBox.align,
      valign: textBox.valign || "top",
      paraSpaceAfter: textBox.paraSpaceAfter,
      margin: textBox.margin,
      fill: opts.background,
    };
  }

  slide.addText(text, textOptions);

  return {
    x,
    y,
    w,
    image: {
      x: imgPlacement?.x ?? x,
      y,
      w: imgPlacement?.w ?? w,
      h: imgPlacement?.h ?? boxH,
    },
    text: { x, y: textY, w, h: hText },
  };
}

function addCardRow(slide, region, cards = [], options = {}) {
  const rx = toNumberOr(region.x, 0.4);
  const ry = toNumberOr(region.y, 1.6);
  const slideWidth = getSlideDimensions(slide).width;
  const rw = toNumberOr(region.w, slideWidth - rx * 2);
  const gap = toNumberOr(options.gap, 0.25);
  const count = cards.length;
  if (count === 0) return [];

  let cardW;
  if (options.widthStrategy === "fixed") {
    cardW = toNumberOr(
      options.cardWidth,
      rw / count - (gap * (count - 1)) / count
    );
  } else {
    cardW = (rw - gap * (count - 1)) / count;
  }

  const totalWidth = cardW * count + gap * (count - 1);
  const align = options.align || "left";
  const ox =
    align === "center"
      ? (rw - totalWidth) / 2
      : align === "right"
      ? rw - totalWidth
      : 0;

  const placements = [];
  for (let i = 0; i < count; i++) {
    const x = rx + ox + i * (cardW + gap);
    placements.push(
      addImageTextCard(slide, { ...cards[i], x, y: ry, width: cardW })
    );
  }
  return placements;
}

function addThreeLevelTree(slide, opts = {}) {
  const slideWidth = getSlideDimensions(slide).width;
  const cx = toNumberOr(opts.centerX, slideWidth / 2);
  const topY = toNumberOr(opts.topY, 1.6);

  const rootW = toNumberOr(opts.root?.w, 3.3333333);
  const rootH = toNumberOr(opts.root?.h, 0.93333333);
  const rootX = cx - rootW / 2;
  const rootFontFaceRaw = opts.root?.fontFace;
  const rootFontFace =
    typeof rootFontFaceRaw === "string" && rootFontFaceRaw.trim().length > 0
      ? rootFontFaceRaw.trim()
      : null;
  if (!rootFontFace) {
    throw new Error(
      "addThreeLevelTree(): opts.root.fontFace is required for text measurement."
    );
  }
  const rootFontSize = toNumberOr(opts.root?.fontSize, 16);
  const rootText = opts.root?.text || "";
  const rootTextOpts = autoFontSize(rootText, rootFontFace, {
    x: rootX,
    y: topY,
    w: rootW,
    h: rootH,
    mode: opts.root?.mode || "shrink",
    fontSize: rootFontSize,
    minFontSize: opts.root?.minFontSize,
    maxFontSize: opts.root?.maxFontSize,
  });
  slide.addText(rootText, {
    ...rootTextOpts,
    align: "center",
    valign: "mid",
    fontFace: rootFontFace,
    color: opts.root?.color || "FFFFFF",
    fill: { color: opts.root?.fill || "0B0F1A" },
    line: { color: opts.root?.line || opts.root?.fill || "0B0F1A" },
  });

  const midLabels = Array.isArray(opts.mid?.labels) ? opts.mid.labels : [];
  const midFontFaceRaw = opts.mid?.fontFace;
  const midFontFace =
    typeof midFontFaceRaw === "string" && midFontFaceRaw.trim().length > 0
      ? midFontFaceRaw.trim()
      : null;
  if (!midFontFace) {
    throw new Error(
      "addThreeLevelTree(): opts.mid.fontFace is required for text measurement."
    );
  }
  let midW = toNumberOr(opts.mid?.w, NaN);
  const midH = toNumberOr(opts.mid?.h, rootH);
  const midY = toNumberOr(opts.mid?.y, topY + rootH + 1.2);
  const requestedSpacing = toNumberOr(opts.mid?.spacing, NaN); // center-to-center distance if provided
  const leftRightMargin = toNumberOr(opts.mid?.marginX, 0.6);
  const availableRowWidth = slideWidth - leftRightMargin * 2;
  const countMid = midLabels.length;
  const minGap = 0.4;
  if (!Number.isFinite(midW) && Number.isFinite(requestedSpacing)) {
    // Derive midW from spacing and available width
    const totalSpan = requestedSpacing * (countMid - 1) + 0; // span between first and last centers
    const maxW = Math.min(rootW, (availableRowWidth - totalSpan) / countMid);
    midW = Math.max(0.8, maxW);
  }
  if (!Number.isFinite(midW)) {
    // Fit equally within available width with minimum gaps
    midW = Math.max(
      0.8,
      (availableRowWidth - minGap * (countMid - 1)) / countMid
    );
  }
  // Compute gap to center-group horizontally without overlap
  let gap = Math.max(
    minGap,
    (availableRowWidth - midW * countMid) / Math.max(1, countMid - 1)
  );
  const totalWidth = midW * countMid + gap * (countMid - 1);
  const startLeft = cx - totalWidth / 2;
  for (let i = 0; i < midLabels.length; i++) {
    const x = startLeft + i * (midW + gap);
    const midText = midLabels[i] || "";
    const midFontSize = toNumberOr(opts.mid?.fontSize, 16);
    const midTextOpts = autoFontSize(midText, midFontFace, {
      x,
      y: midY,
      w: midW,
      h: midH,
      mode: opts.mid?.mode || "shrink",
      fontSize: midFontSize,
      minFontSize: opts.mid?.minFontSize,
      maxFontSize: opts.mid?.maxFontSize,
    });
    slide.addText(midText, {
      ...midTextOpts,
      align: "center",
      valign: "mid",
      fontFace: midFontFace,
      color: opts.mid?.color || "000000",
      fill: { color: opts.mid?.fill || "A0BEC2" },
      line: { color: opts.mid?.line || opts.mid?.fill || "A0BEC2" },
    });
    addConnector(slide, cx, topY + rootH, x + midW / 2, midY, opts.line);
  }

  const leavesPerMid = Array.isArray(opts.leaf?.labelsPerMid)
    ? opts.leaf.labelsPerMid
    : [];
  const leafFontFaceRaw = opts.leaf?.fontFace;
  const leafFontFace =
    typeof leafFontFaceRaw === "string" && leafFontFaceRaw.trim().length > 0
      ? leafFontFaceRaw.trim()
      : null;
  if (!leafFontFace) {
    throw new Error(
      "addThreeLevelTree(): opts.leaf.fontFace is required for text measurement."
    );
  }
  const leafW = toNumberOr(opts.leaf?.w, 1.05);
  const leafH = toNumberOr(opts.leaf?.h, 1.0666667);
  const leafY = toNumberOr(opts.leaf?.y, midY + midH + 1.0);
  const minLeafGap = 0.2;
  for (let i = 0; i < midLabels.length; i++) {
    const xBase = startLeft + i * (midW + gap);
    const childLabels = Array.isArray(leavesPerMid[i]) ? leavesPerMid[i] : [];
    const childCount = childLabels.length || 3;
    // Compute per-mid gap to fit children within midW without overlap
    const leafGap = Math.max(
      minLeafGap,
      (midW - childCount * leafW) / Math.max(1, childCount - 1)
    );
    const totalWidth = childCount * leafW + (childCount - 1) * leafGap;
    const leftX = xBase + (midW - totalWidth) / 2;
    for (let j = 0; j < childCount; j++) {
      const x = leftX + j * (leafW + leafGap);
      const leafText = childLabels[j] || "";
      const leafFontSize = toNumberOr(opts.leaf?.fontSize, 16);
      const leafTextOpts = autoFontSize(leafText, leafFontFace, {
        x,
        y: leafY,
        w: leafW,
        h: leafH,
        mode: opts.leaf?.mode || "shrink",
        fontSize: leafFontSize,
        minFontSize: opts.leaf?.minFontSize,
        maxFontSize: opts.leaf?.maxFontSize,
      });
      slide.addText(leafText, {
        ...leafTextOpts,
        align: "center",
        valign: "mid",
        fontFace: leafFontFace,
        color: opts.leaf?.color || "000000",
        fill: { color: opts.leaf?.fill || "A6C1EE" },
        line: { color: opts.leaf?.line || opts.leaf?.fill || "A6C1EE" },
      });
      addConnector(
        slide,
        xBase + midW / 2,
        midY + midH,
        x + leafW / 2,
        leafY,
        opts.line
      );
    }
  }
}

function addConnector(slide, x1, y1, x2, y2, line = {}) {
  const x = Math.min(x1, x2);
  const y = Math.min(y1, y2);
  slide.addShape("line", {
    x,
    y,
    w: Math.abs(x2 - x1),
    h: Math.abs(y2 - y1),
    line: { color: line.color || "000000", pt: line.pt || 1 },
    flipH: x2 < x1 ? true : undefined,
  });
}

function toNumberOr(v, fallback) {
  const n = typeof v === "string" ? parseFloat(v) : v;
  return Number.isFinite(n) ? n : fallback;
}
