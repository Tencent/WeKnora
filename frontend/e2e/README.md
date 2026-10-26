# Browser regression tests

Run `npm ci`, `npx playwright install --with-deps chromium`, `npm run type-check:e2e`, then `npm run test:e2e`. `PLAYWRIGHT_PORT` selects the dedicated local port (default 5186, strict port binding). `PLAYWRIGHT_CHROMIUM_EXECUTABLE` can point to an already installed Chromium binary.

Phone cases use `isMobile: true` and `hasTouch: true`, honoring the viewport meta tag. Tests tagged `@desktop` use a separate mouse/desktop context. Fixtures initialize authentication and dismiss onboarding, but do not set sidebar preferences. Unknown API requests fail explicitly instead of receiving empty success responses. All data is local test data; no production API is contacted.

This infrastructure proposal adds one desktop startup smoke test. Page-specific behavior and mobile regressions accompany their respective feature PRs. Layout assertions requiring overlay navigation should be applied after #4041. Device emulation cannot prove physical iOS keyboard or Safari behavior.

The CI wiring is proposed separately here for maintainer discussion. It is not part of the chat or mobile feature PRs.
