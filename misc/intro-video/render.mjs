// Renders index.html frame by frame into an MP4 (or a few PNG stills).
//
//   node render.mjs video [out.mp4]      full video, 1920x1080 @ 30fps
//   node render.mjs stills 9.5 22 57     PNG stills at the given seconds, into stills/
//
// The page exposes render(t) and DUR; every frame is drawn from t alone, so
// renders are deterministic and any frame can be inspected in isolation.
// Set CHROME_PATH to use a specific Chromium build instead of Playwright's.
import { chromium } from 'playwright-core';
import { spawn } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const [mode = 'video', ...rest] = process.argv.slice(2);
const FPS = 30;

const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || undefined });
const page = await browser.newPage({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 });
page.on('pageerror', (e) => console.error('pageerror:', e.message));
page.on('console', (m) => { if (m.type() === 'error') console.error('console:', m.text()); });
await page.goto(pathToFileURL(join(here, 'index.html')).href);
await page.evaluate(() => document.fonts.ready);

if (mode === 'stills') {
  mkdirSync(join(here, 'stills'), { recursive: true });
  for (const t of rest.map(Number)) {
    await page.evaluate((t) => window.render(t), t);
    await page.screenshot({ path: join(here, 'stills', `t${t.toFixed(2).padStart(7, '0')}.png`) });
  }
} else if (mode === 'video') {
  const out = rest[0] || join(here, 'weknora-intro.mp4');
  const dur = await page.evaluate(() => window.DUR);
  const ff = spawn('ffmpeg', ['-y', '-f', 'image2pipe', '-framerate', String(FPS), '-i', '-',
    '-c:v', 'libx264', '-preset', 'slow', '-crf', '17', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', out],
  { stdio: ['pipe', 'ignore', 'inherit'] });
  const n = Math.round(dur * FPS);
  for (let f = 0; f < n; f++) {
    await page.evaluate((t) => window.render(t), f / FPS);
    const png = await page.screenshot({ type: 'png' });
    if (!ff.stdin.write(png)) await new Promise((r) => ff.stdin.once('drain', r));
    if (f % 300 === 0) console.log(`frame ${f}/${n}`);
  }
  ff.stdin.end();
  const code = await new Promise((r) => ff.on('close', r));
  if (code !== 0) { console.error(`ffmpeg exited with ${code}`); process.exitCode = 1; } else console.log(`wrote ${out}`);
} else {
  console.error(`unknown mode "${mode}", expected "video" or "stills"`);
  process.exitCode = 2;
}
await browser.close();
