# Local browser regressions

Use Node 22+, then `npm ci` and `npx playwright install chromium`.
Run `npm run test:e2e`. No backend, account, credentials or paid model calls are used;
API responses are isolated fixtures. CI setup is intentionally unchanged.

The noVNC regression replaces `VideoDecoder.isConfigSupported` with a promise that
never settles, then checks first-send navigation, reply rendering and direct reload.
To reproduce the pre-fix failure, restore the static RFB import in
`src/composables/useSandboxDesktop.ts` and remove the dynamic import in `openDesktop`.
On upstream main a729d1fc the navigation assertion times out after 15 seconds;
with the lazy import the same test passes without invoking the decoder probe.

`PLAYWRIGHT_CHROMIUM_EXECUTABLE` can select an already installed Chromium binary.
