import assert from 'node:assert/strict';
import { readFile, readdir } from 'node:fs/promises';
import { dirname, resolve, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { resolveSiteFile } from './site-files.mjs';
const project = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const root = resolve(process.argv[2] || resolve(project, 'static-site'));
async function walk(dir) {
  const result = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = resolve(dir, entry.name);
    if (entry.isDirectory()) result.push(...await walk(path));
    else if (entry.name.endsWith('.html')) result.push(path);
  }
  return result;
}
for (const retired of ['/demo/', '/demo/1/', '/redesign/', '/redesign/1/', '/demo6/preview.html', '/brand/weknora.svg', '/icon.svg', '/docs/favicon.svg', '/docs/README.html', '/docs/homepage/README.html', '/docs/homepage/BRAND-ASSETS.html', '/docs/static-site/', '/docs/releases/']) {
  assert.equal(await resolveSiteFile(root, retired), null, `Retired demo must not be published: ${retired}`);
}
const failures = [];
// Screenshot.vue renders a labelled placeholder until the image is added, so a
// missing docs screenshot is reported as pending instead of failing the build.
const pendingShots = new Set();
const checked = new Map();
const pages = await walk(root);
for (const file of pages) {
  const html = await readFile(file, 'utf8');
  const route = '/' + relative(root, file).replaceAll('\\', '/');
  const base = new URL(route, 'https://weknora.test');
  for (const match of html.matchAll(/<(?:a|link|img|script|source|video)\b[^>]*>/g)) {
    const tag = match[0];
    for (const [, raw] of tag.matchAll(/(?:href|src|poster)="([^"]+)"/g)) {
      const url = new URL(raw.replaceAll('&amp;', '&'), base);
      if (url.origin !== base.origin) continue;
      if (!checked.has(url.pathname)) checked.set(url.pathname, !!await resolveSiteFile(root, url.pathname));
      if (!checked.get(url.pathname)) {
        if (/class="wk-shot-img"/.test(tag) && url.pathname.startsWith('/docs/screenshots/')) pendingShots.add(url.pathname);
        else failures.push(`${route}: missing ${url.pathname}`);
      }
      if (url.pathname === '/' && tag.startsWith('<a') && /target="_blank"/.test(tag)) failures.push(`${route}: homepage link opens a new tab`);
    }
  }
  if (/11\.141\.160\.83/.test(html)) failures.push(`${route}: stale private docs host`);
  if (route.startsWith('/docs/') && !route.endsWith('/404.html') && !/class="wk-brand"[^>]*href="\/"[^>]*target="_self"/.test(html)) failures.push(`${route}: documentation logo must navigate to the main site`);
}
for (const path of ['/docs/', '/docs/03-features/22-skills-sandbox.html', '/docs/03-features/23-memory.html', '/docs/07-releases/v0.8.2.html', '/docs/07-releases/v0.8.0.html']) assert.ok(await resolveSiteFile(root, path), path);
assert.equal(failures.length, 0, [...new Set(failures)].join('\n'));
if (pendingShots.size) console.warn(`Screenshots still pending (placeholder shown): ${[...pendingShots].sort().join(', ')}`);
console.log(`Unified site check passed: ${pages.length} pages, ${checked.size} local routes/assets, same-tab homepage links and documentation logos.`);
