import type { NextConfig } from "next";
import { resolve } from "node:path";
import { homeAssets } from "../shared/header";

const nextConfig: NextConfig = {
  turbopack: { root: resolve(__dirname, "..") },
  output: "export",
  // Chunks are requested from /docs/_home/_next/; build-site.mjs moves out/_next there.
  assetPrefix: homeAssets,
  trailingSlash: true,
  images: {
    unoptimized: true,
    remotePatterns: [
      {
        protocol: "https",
        hostname: "images.unsplash.com",
      },
    ],
  },
};

export default nextConfig;
