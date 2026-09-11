// Copyright (c) OpenAI. All rights reserved.
"use strict";

// Safe outer shadow helper (avoid inner/outer mix and XML pitfalls)
function safeOuterShadow(
  color = "000000",
  opacity = 0.25,
  angle = 45,
  blur = 3,
  offset = 2
) {
  return {
    type: "outer",
    color,
    opacity,
    angle,
    blur,
    offset,
  };
}

module.exports = {
  safeOuterShadow,
};
