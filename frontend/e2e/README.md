# Browser validation

Apply the shared browser infrastructure PR #4141 before running these tests. The dependency owns Playwright, the strict common fixtures, device projects and CI wiring; this page PR contains only its relevant regressions.

Run `npm run test:e2e` locally with Chromium installed. The phone project sets `isMobile: true` and `hasTouch: true`; desktop cases use a separate context. Fixtures intentionally do not set `sidebar_collapsed`, and unknown API requests fail instead of receiving a success response.

This page suite requires the navigation/viewport base in Tencent/WeKnora#4041; apply that PR before testing settings. No CI workflow is changed in this PR. Device emulation proves layout and touch-event handling, not physical iOS keyboard or Safari behavior.
