// Copyright (c) OpenAI. All rights reserved.
"use strict";

function toDataUri(svg) {
  return "data:image/svg+xml;base64," + Buffer.from(svg).toString("base64");
}

function sanitizeSvg(svg) {
  let inner = svg;
  const a = inner.indexOf("<svg");
  const b = inner.indexOf("</svg>");
  if (a !== -1 && b !== -1) inner = inner.slice(a, b + 6);
  inner = inner.replace(/<\?xml[^>]*>/g, "");
  if (!/xmlns=\"http:\/\/www\.w3\.org\/2000\/svg\"/.test(inner)) {
    inner = inner.replace(/<svg /, '<svg xmlns="http://www.w3.org/2000/svg" ');
  }
  inner = inner.replace(
    /(width|height)=\"([0-9.]+)(ex|em)\"/g,
    (_m, attr, num) => {
      const px = Math.round(parseFloat(num) * 8.5);
      return `${attr}="${px}px"`;
    }
  );
  inner = inner.replace(/currentColor/g, "#000000");
  return inner;
}

function svgToDataUri(svg) {
  return toDataUri(sanitizeSvg(svg));
}

module.exports = {
  toDataUri,
  sanitizeSvg,
  svgToDataUri,
};
