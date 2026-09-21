// T00 冒烟：前端可达 + 后端健康 + 首页可渲染截图
const { chromium } = require('playwright');
(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  const resp = await page.goto('http://localhost:5173/', { waitUntil: 'domcontentloaded', timeout: 30000 });
  const status = resp.status();
  await page.screenshot({ path: 'artifacts/smoke-home.png', fullPage: true });
  const title = await page.title();
  console.log(JSON.stringify({ frontendStatus: status, title }));
  const health = await page.request.get('http://localhost:8080/health');
  console.log(JSON.stringify({ backendHealth: health.status() }));
  await browser.close();
  if (status !== 200 || health.status() !== 200) process.exit(1);
})();
