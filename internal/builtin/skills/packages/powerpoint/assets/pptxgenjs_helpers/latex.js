// Copyright (c) OpenAI. All rights reserved.
"use strict";

let _mathjax;
let _adaptor;
let _doc;

function ensureMathJax() {
  if (_mathjax && _adaptor && _doc) return;
  try {
    const { mathjax } = require("mathjax-full/js/mathjax.js");
    const { TeX } = require("mathjax-full/js/input/tex.js");
    const { SVG } = require("mathjax-full/js/output/svg.js");
    const { liteAdaptor } = require("mathjax-full/js/adaptors/liteAdaptor.js");
    const { RegisterHTMLHandler } = require("mathjax-full/js/handlers/html.js");
    const { AllPackages } = require("mathjax-full/js/input/tex/AllPackages.js");

    _adaptor = liteAdaptor();
    RegisterHTMLHandler(_adaptor);
    const tex = new TeX({ packages: AllPackages });
    const out = new SVG({ fontCache: "local" });
    _doc = mathjax.document("", { InputJax: tex, OutputJax: out });
    _mathjax = mathjax;
  } catch (err) {
    throw new Error(
      "mathjax-full is not installed. Run `npm i mathjax-full` or avoid latexToSvgDataUri()."
    );
  }
}

function latexToSvgDataUri(latex, display = true) {
  ensureMathJax();
  const html = _adaptor.outerHTML(_doc.convert(latex, { display }));
  const a = html.indexOf("<svg");
  const b = html.indexOf("</svg>");
  let svg = a !== -1 && b !== -1 ? html.slice(a, b + 6) : html;
  svg = svg.replace(/<\?xml[^>]*>/g, "");
  if (!/xmlns=\"http:\/\/www\.w3\.org\/2000\/svg\"/.test(svg)) {
    svg = svg.replace(/<svg /, '<svg xmlns="http://www.w3.org/2000/svg" ');
  }
  svg = svg.replace(/(width|height)=\"([0-9.]+)(ex|em)\"/g, (_m, attr, num) => {
    const px = Math.round(parseFloat(num) * 8.5);
    return `${attr}="${px}px"`;
  });
  svg = svg.replace(/currentColor/g, "#000000");
  return "data:image/svg+xml;base64," + Buffer.from(svg).toString("base64");
}

module.exports = {
  latexToSvgDataUri,
};
