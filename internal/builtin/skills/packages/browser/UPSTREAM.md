# Browser runtime

Browser automation uses vercel-labs/agent-browser v0.37.1, commit
72007a6788d863611b23bed0b59d0d659c638d8e (Apache-2.0).
UPSTREAM_SKILL.md, upstream-core/ and LICENSE are unmodified upstream files tracked in sources.lock.json. The core SKILL.md is stored as CORE.md to keep a single skill entry in the archive; installation restores its upstream filename in the runtime directory.
The native Linux AMD64/ARM64 musl executables are downloaded at installation;
runtime.lock.json pins their release URLs and SHA-256 digests. Binaries are not vendored.

The installed CLI serves the vendored version-matched guide with `skills get core`.
Standalone release binaries do not embed this guide, so installation includes it explicitly.
WeKnora's SKILL.md supplies sandbox paths and session ownership rules. The
Python adapter serializes native CLI calls with Browser panel actions
and enforces the human-control lease; it contains no Playwright/browser driver.
The live preview bridge uses hash-pinned websocket-client 1.9.0 for transport.
The installer uses an existing Chrome/Chromium or Debian's Chromium package and
records the actual browser version in .weknora/runtime.json. The sandbox snapshot
pins that installed environment; source/package changes do not modify old snapshots.
