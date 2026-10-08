# Local browser regressions

Run `npm ci`, `npx playwright install chromium`, then `CI=true npm run test:e2e`.
Tests use isolated mock API data, with no real credentials or paid model calls.
CI workflow setup is intentionally unchanged.

`PLAYWRIGHT_PORT` selects a local port (default 5186); the server fails immediately
if that port is occupied instead of silently testing another service.
`PLAYWRIGHT_CHROMIUM_EXECUTABLE` selects an already installed Chromium binary.
The noVNC main-fail/fix-pass reproduction, when included, is documented in `novnc.md`.
