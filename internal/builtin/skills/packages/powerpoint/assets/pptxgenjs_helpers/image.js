// Copyright (c) OpenAI. All rights reserved.
"use strict";

const fs = require("fs");

// Accept either a filesystem path, a data URI, raw SVG string, or a Buffer
// and normalize to a Buffer for type/size probing.
function readInputAsBuffer(source) {
  if (!source) throw new Error("Image source is empty");
  if (Buffer.isBuffer(source)) return { buffer: source, type: "buffer" };
  if (typeof source === "string") {
    // data URI (we primarily emit base64 data URIs for SVG via helpers)
    if (source.startsWith("data:")) {
      const type = "dataUri";
      const comma = source.indexOf(",");
      const payload = comma !== -1 ? source.slice(comma + 1) : source;
      // Our helpers use base64; if not, try URI decode then treat as raw text
      try {
        return { buffer: Buffer.from(payload, "base64"), type: type };
      } catch (_e) {
        try {
          return {
            buffer: Buffer.from(decodeURIComponent(payload), "utf8"),
            type: type,
          };
        } catch (_e2) {
          return { buffer: Buffer.from(payload, "utf8"), type: type };
        }
      }
    }
    // Raw inline SVG string
    if (source.includes("<svg")) {
      return { buffer: Buffer.from(source, "utf8"), type: "rawSvg" };
    }
    // Treat as filesystem path
    return { buffer: fs.readFileSync(source), type: "path" };
  }
  throw new Error("Unsupported image source type");
}

function isPng(buf) {
  return (
    buf.length >= 24 &&
    buf[0] === 0x89 &&
    buf[1] === 0x50 &&
    buf[2] === 0x4e &&
    buf[3] === 0x47 &&
    buf[4] === 0x0d &&
    buf[5] === 0x0a &&
    buf[6] === 0x1a &&
    buf[7] === 0x0a
  );
}

function isJpeg(buf) {
  return (
    buf.length > 3 && buf[0] === 0xff && buf[1] === 0xd8 && buf[2] === 0xff
  );
}

function isGif(buf) {
  return (
    buf.length >= 10 &&
    buf[0] === 0x47 &&
    buf[1] === 0x49 &&
    buf[2] === 0x46 &&
    buf[3] === 0x38 &&
    (buf[4] === 0x39 || buf[4] === 0x37) &&
    buf[5] === 0x61
  );
}

function isWebp(buf) {
  return (
    buf.length >= 16 &&
    buf[0] === 0x52 &&
    buf[1] === 0x49 &&
    buf[2] === 0x46 &&
    buf[3] === 0x46 &&
    buf[8] === 0x57 &&
    buf[9] === 0x45 &&
    buf[10] === 0x42 &&
    buf[11] === 0x50
  );
}

function isSvg(buf) {
  const head = buf.slice(0, 200).toString("utf8");
  return head.includes("<svg");
}

function readPngSize(buf) {
  // IHDR chunk: width/height at offset 16, big-endian
  const width = buf.readUInt32BE(16);
  const height = buf.readUInt32BE(20);
  return { width, height, type: "png" };
}

function readGifSize(buf) {
  const width = buf.readUInt16LE(6);
  const height = buf.readUInt16LE(8);
  return { width, height, type: "gif" };
}

function readWebpSize(buf) {
  // Handle VP8X, VP8L, VP8
  // Reference: https://developers.google.com/speed/webp/docs/riff_container
  const riffSize = buf.readUInt32LE(4) + 8;
  let offset = 12; // start of first chunk
  while (offset + 8 <= riffSize && offset + 8 <= buf.length) {
    const chunkTag = buf.slice(offset, offset + 4).toString("ascii");
    const chunkSize = buf.readUInt32LE(offset + 4);
    if (chunkTag === "VP8X") {
      // Canvas size stored at bytes 12..17 (6 bytes), 24 bits each minus 1
      const wMinus1 = buf.readUIntLE(offset + 12, 3);
      const hMinus1 = buf.readUIntLE(offset + 15, 3);
      return { width: wMinus1 + 1, height: hMinus1 + 1, type: "webp" };
    }
    if (chunkTag === "VP8 ") {
      // Lossy bitstream: frame header at start of data
      // Parse minimally for width/height
      const start = offset + 8;
      if (start + 10 < buf.length) {
        const width = buf.readUInt16LE(start + 6) & 0x3fff;
        const height = buf.readUInt16LE(start + 8) & 0x3fff;
        return { width, height, type: "webp" };
      }
    }
    if (chunkTag === "VP8L") {
      // Lossless bitstream: 14-bit width/height encoded
      const start = offset + 8;
      if (start + 5 <= buf.length) {
        const b0 = buf[start + 1];
        const b1 = buf[start + 2];
        const b2 = buf[start + 3];
        const b3 = buf[start + 4];
        const width = 1 + (((b1 & 0x3f) << 8) | b0);
        const height =
          1 + (((b3 & 0xf) << 10) | (b2 << 2) | ((b1 & 0xc0) >> 6));
        return { width, height, type: "webp" };
      }
    }
    offset += 8 + ((chunkSize + 1) & ~1); // chunks are padded to even size
  }
  throw new Error("Unsupported WEBP variant for size detection");
}

function readJpegSize(buf) {
  let offset = 2;
  while (offset < buf.length) {
    if (buf[offset] !== 0xff) {
      offset++;
      continue;
    }
    const marker = buf[offset + 1];
    // SOF0..SOF3, SOF5..SOF7, SOF9..SOF11, SOF13..SOF15
    if (
      (marker >= 0xc0 && marker <= 0xc3) ||
      (marker >= 0xc5 && marker <= 0xc7) ||
      (marker >= 0xc9 && marker <= 0xcb) ||
      (marker >= 0xcd && marker <= 0xcf)
    ) {
      const blockLength = buf.readUInt16BE(offset + 2);
      const height = buf.readUInt16BE(offset + 5);
      const width = buf.readUInt16BE(offset + 7);
      return { width, height, type: "jpeg" };
    }
    const blockLength = buf.readUInt16BE(offset + 2);
    if (!Number.isFinite(blockLength) || blockLength < 2) break;
    offset += 2 + blockLength;
  }
  throw new Error("JPEG size not found");
}

function parseSvgSize(buf) {
  const text = buf.toString("utf8");
  const a = text.indexOf("<svg");
  const b = text.indexOf("</svg>");
  const inner = a !== -1 && b !== -1 ? text.slice(a, b + 6) : text;
  const widthMatch = inner.match(/\bwidth\s*=\s*"([^"]+)"/i);
  const heightMatch = inner.match(/\bheight\s*=\s*"([^"]+)"/i);
  const viewBoxMatch = inner.match(/\bviewBox\s*=\s*"([^"]+)"/i);

  function toPx(v) {
    if (!v) return null;
    const m = String(v)
      .trim()
      .match(/([0-9.]+)\s*(px|pt|em|ex|cm|mm|in|%)?/i);
    if (!m) return null;
    const n = parseFloat(m[1]);
    const unit = (m[2] || "px").toLowerCase();
    const dpi = 96;
    switch (unit) {
      case "px":
        return n;
      case "pt":
        return (n * dpi) / 72;
      case "in":
        return n * dpi;
      case "cm":
        return (n * dpi) / 2.54;
      case "mm":
        return (n * dpi) / 25.4;
      case "em":
      case "ex":
        return n * 16; // rough fallback
      default:
        return null;
    }
  }

  let widthPx = widthMatch ? toPx(widthMatch[1]) : null;
  let heightPx = heightMatch ? toPx(heightMatch[1]) : null;
  if ((widthPx == null || heightPx == null) && viewBoxMatch) {
    const parts = viewBoxMatch[1].trim().split(/\s+/).map(Number);
    if (parts.length === 4) {
      const vbw = parts[2];
      const vbh = parts[3];
      if (!widthPx && vbh) widthPx = vbw;
      if (!heightPx && vbw) heightPx = vbh;
    }
  }
  if (!widthPx || !heightPx) {
    // Fallback if sizes missing
    widthPx = widthPx || 100;
    heightPx = heightPx || 100;
  }
  return { width: widthPx, height: heightPx, type: "svg" };
}

function getImageDimensions(pathOrData) {
  const { buffer: buf, type } = readInputAsBuffer(pathOrData);
  let meta;
  if (isPng(buf)) meta = readPngSize(buf);
  else if (isJpeg(buf)) meta = readJpegSize(buf);
  else if (isGif(buf)) meta = readGifSize(buf);
  else if (isWebp(buf)) meta = readWebpSize(buf);
  else if (isSvg(buf)) meta = parseSvgSize(buf);
  else {
    const suffix =
      type === "path" && typeof pathOrData === "string"
        ? ` (path: ${pathOrData})`
        : "";
    throw new Error("Unsupported image format for provided source" + suffix);
  }

  const aspectRatio =
    meta.width > 0 && meta.height > 0 ? meta.width / meta.height : 1;
  return {
    width: meta.width,
    height: meta.height,
    aspectRatio,
    type: meta.type,
  };
}

function imageSizingCrop(source, x, y, w, h, cx, cy, cw, ch) {
  const { aspectRatio } = getImageDimensions(source);
  const boxAspect = w / h;

  if (
    cx === undefined ||
    cy === undefined ||
    cw === undefined ||
    ch === undefined
  ) {
    let cropXFrac, cropYFrac, cropWFrac, cropHFrac;
    if (aspectRatio >= boxAspect) {
      cropHFrac = 1;
      cropWFrac = boxAspect / aspectRatio;
      cropXFrac = (1 - cropWFrac) / 2;
      cropYFrac = 0;
    } else {
      cropWFrac = 1;
      cropHFrac = aspectRatio / boxAspect;
      cropXFrac = 0;
      cropYFrac = (1 - cropHFrac) / 2;
    }
    cx = cropXFrac;
    cy = cropYFrac;
    cw = cropWFrac;
    ch = cropHFrac;
  }

  let virtualW = w / cw;
  let virtualH = virtualW / aspectRatio;
  const eps = 1e-6;
  if (Math.abs(virtualH * ch - h) > eps) {
    virtualH = h / ch;
    virtualW = virtualH * aspectRatio;
  }

  const cropXIn = cx * virtualW;
  const cropYIn = cy * virtualH;
  return {
    x,
    y,
    w: virtualW,
    h: virtualH,
    sizing: {
      type: "crop",
      x: cropXIn,
      y: cropYIn,
      w: w,
      h: h,
    },
  };
}

function imageSizingContain(source, x, y, w, h) {
  const { aspectRatio } = getImageDimensions(source);
  let w2, h2;
  const boxAspect = w / h;
  if (aspectRatio >= boxAspect) {
    w2 = w;
    h2 = w2 / aspectRatio;
  } else {
    h2 = h;
    w2 = h2 * aspectRatio;
  }
  return {
    x: x + (w - w2) / 2,
    y: y + (h - h2) / 2,
    w: w2,
    h: h2,
  };
}

module.exports = {
  getImageDimensions,
  imageSizingCrop,
  imageSizingContain,
};
