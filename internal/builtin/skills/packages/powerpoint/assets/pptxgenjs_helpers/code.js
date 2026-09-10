// Copyright (c) OpenAI. All rights reserved.
"use strict";

const fs = require("fs");
const Prism = require("prismjs");
let THEME_MAP;

function loadPrismLanguage(lang) {
  const normalized = String(lang || "plaintext").toLowerCase();
  const known = new Set([
    "markup",
    "html",
    "xml",
    "svg",
    "mathml",
    "css",
    "clike",
    "javascript",
    "js",
    "typescript",
    "ts",
    "python",
    "py",
    "bash",
    "sh",
    "json",
    "yaml",
    "yml",
  ]);
  const map = {
    js: "javascript",
    ts: "typescript",
    py: "python",
    sh: "bash",
    yml: "yaml",
    html: "markup",
    xml: "markup",
  };
  const id = map[normalized] || normalized;
  if (!Prism.languages[id]) {
    try {
      require(`prismjs/components/prism-${id}`);
    } catch (_e) {}
  }
  return Prism.languages[id] || Prism.languages.plain || {};
}

function buildThemeMap(themeCssModule = "prismjs/themes/prism-okaidia.css") {
  try {
    const css = fs.readFileSync(require.resolve(themeCssModule), "utf8");
    return Object.fromEntries(
      [
        ...css.matchAll(
          /\.token\.([\w-]+)[^{]*\{[^}]*color:\s*([^;\s]+)[^}]*\}/g
        ),
      ].map(([, t, c]) => [t, c.replace(/#|!important/g, "").trim()])
    );
  } catch (err) {
    return { plain: "FFFFFF", comment: "999999" };
  }
}

function getThemeMap() {
  if (!THEME_MAP) THEME_MAP = buildThemeMap();
  return THEME_MAP;
}

function run(text, type = "plain") {
  const theme = getThemeMap();
  return {
    text,
    options: {
      fontFace: "Consolas",
      color: theme[type] || theme.plain || "FFFFFF",
      fontSize: 14,
    },
  };
}

function tokensToRuns(tokens) {
  return tokens.flatMap((t) =>
    typeof t === "string"
      ? [run(t)]
      : Array.isArray(t.content)
      ? tokensToRuns(t.content)
      : [run(t.content, t.type)]
  );
}

function codeToRuns(code, lang) {
  const grammar = loadPrismLanguage(lang);
  const lines = String(code || "").split("\n");
  const pad = lines.length.toString().length;
  return lines.flatMap((line, i) => [
    run(`${(i + 1).toString().padStart(pad, " ")} `, "comment"),
    ...tokensToRuns(Prism.tokenize(line, grammar)),
    ...(i < lines.length - 1 ? [run("\n")] : []),
  ]);
}

module.exports = {
  codeToRuns,
  buildThemeMap,
};
