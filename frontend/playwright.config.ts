import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  expect: { timeout: 15000 },
  fullyParallel: false,
  workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:5186',
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE } : {},
    screenshot: 'only-on-failure',
  },
  webServer: {
    command: 'npm run dev -- --host 127.0.0.1 --port 5186',
    url: 'http://127.0.0.1:5186',
    reuseExistingServer: !process.env.CI,
  },
})
