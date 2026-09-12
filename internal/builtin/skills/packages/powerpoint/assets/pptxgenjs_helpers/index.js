// Copyright (c) OpenAI. All rights reserved.
"use strict";

const VERSION = "1.2.0";

const text = require("./text");
const image = require("./image");
const svg = require("./svg");
const latex = require("./latex");
const code = require("./code");
const layout = require("./layout");
const layoutBuilders = require("./layout_builders");
const util = require("./util");

module.exports = {
  VERSION,
  // text layout
  ...text,
  // images
  ...image,
  // svg helpers
  ...svg,
  // LaTeX -> SVG
  ...latex,
  // code block -> pptx text runs
  ...code,
  // slide layout analyzers
  ...layout,
  // slide layout builders
  ...layoutBuilders,
  // text layout helpers and utilities
  ...util,
};
