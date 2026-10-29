// The coherent profile claims only what this Chrome backs: a Linux desktop of
// the CPU architecture it runs on, 8 logical cores, en-US. WebGL is never
// touched; the image renders it on SwiftShader and no real GPU can be
// reproduced from outside the browser.
const CPU_PROFILES = Object.freeze({
  x64: { machine: "x86_64", architecture: "x86" },
  arm64: { machine: "aarch64", architecture: "arm" },
});

export function cpuProfile(arch = process.arch) {
  return CPU_PROFILES[arch] ?? CPU_PROFILES.x64;
}

export const LINUX_PROFILE = Object.freeze({
  navigatorPlatform: `Linux ${cpuProfile().machine}`,
  hardwareConcurrency: 8,
  acceptLanguage: "en-US,en",
});

// Chromium seeds the GREASE brand, its version, and the brand order with the major version (https://chromium.googlesource.com/chromium/src/+/refs/tags/154.0.8037.57/components/embedder_support/user_agent_utils.cc).
const GREASE_CHARS = [" ", "(", ":", "-", ".", "/", ")", ";", "=", "?", "_"];
const GREASE_VERSIONS = ["8", "99", "24"];
const BRAND_ORDERS = [
  [0, 1, 2],
  [0, 2, 1],
  [1, 0, 2],
  [1, 2, 0],
  [2, 0, 1],
  [2, 1, 0],
];

export function chromeVersionParts(version) {
  const m = /^(\d+)\.(\d+)\.(\d+)\.(\d+)$/.exec(version ?? "");
  if (m == null) return { major: "0", full: "0.0.0.0" };
  return { major: m[1], full: version };
}

export function greaseBrandVersion(major) {
  const seed = Number.parseInt(major, 10);
  const brand = `Not${GREASE_CHARS[seed % GREASE_CHARS.length]}A${GREASE_CHARS[(seed + 1) % GREASE_CHARS.length]}Brand`;
  const short = GREASE_VERSIONS[seed % GREASE_VERSIONS.length];
  return { brand, short, full: `${short}.0.0.0` };
}

function shuffleBrands(major, entries) {
  const order = BRAND_ORDERS[Number.parseInt(major, 10) % BRAND_ORDERS.length];
  const shuffled = new Array(entries.length);
  for (let i = 0; i < order.length; i++) shuffled[order[i]] = entries[i];
  return shuffled;
}

function userAgentMetadata({ major, full }, cpu) {
  const grease = greaseBrandVersion(major);
  return {
    brands: shuffleBrands(major, [
      { brand: grease.brand, version: grease.short },
      { brand: "Chromium", version: major },
      { brand: "Google Chrome", version: major },
    ]),
    fullVersionList: shuffleBrands(major, [
      { brand: grease.brand, version: grease.full },
      { brand: "Chromium", version: full },
      { brand: "Google Chrome", version: full },
    ]),
    fullVersion: full,
    platform: "Linux",
    platformVersion: "",
    architecture: cpu.architecture,
    model: "",
    mobile: false,
    bitness: "64",
    wow64: false,
    formFactors: ["Desktop"],
  };
}

// The version always comes from the running browser, so the UA never claims a
// release other than the one that answers.
export function buildUserAgentOverride(chromeVersion, arch = process.arch) {
  const parts = chromeVersionParts(chromeVersion);
  const cpu = cpuProfile(arch);
  return {
    userAgent: `Mozilla/5.0 (X11; Linux ${cpu.machine}) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${parts.major}.0.0.0 Safari/537.36`,
    acceptLanguage: LINUX_PROFILE.acceptLanguage,
    platform: `Linux ${cpu.machine}`,
    userAgentMetadata: userAgentMetadata(parts, cpu),
  };
}
