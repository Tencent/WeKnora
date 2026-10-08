# Local browser regressions

Run `npm ci`, `npx playwright install chromium`, then `npm run test:e2e`.
Tests use isolated mock API data and no real credentials or paid model calls.
CI setup is intentionally unchanged. `PLAYWRIGHT_CHROMIUM_EXECUTABLE` can select an installed Chromium.
