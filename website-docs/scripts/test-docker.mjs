import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { setTimeout } from 'node:timers/promises';

const image = process.argv[2] || 'weknora-site:0.8.0';
const name = `weknora-site-test-${randomUUID()}`;
function docker(...args) {
  const result = spawnSync('docker', args, { encoding: 'utf8' });
  if (result.error) throw result.error;
  assert.equal(result.status, 0, `docker ${args.join(' ')}\n${result.stderr}`);
  return result.stdout.trim();
}

try {
  docker('run', '-d', '--name', name, '-p', '127.0.0.1::80', image);
  const origin = `http://${docker('port', name, '80/tcp')}`;
  docker('exec', name, 'nginx', '-t');
  docker('exec', name, 'sh', '-c', '! command -v node && test ! -d /build && test ! -e /usr/share/nginx/html/package.json');
  let ready = false;
  for (let attempt = 0; attempt < 30; attempt++) {
    try {
      ready = (await fetch(origin, { signal: AbortSignal.timeout(2000) })).ok;
      if (ready) break;
    } catch { /* Nginx may still be starting. */ }
    await setTimeout(200);
  }
  assert.ok(ready, 'Container did not become ready');

  const redirect = await fetch(`${origin}/docs`, { redirect: 'manual' });
  assert.equal(redirect.status, 308);
  assert.equal(new URL(redirect.headers.get('location'), origin).origin, origin);
  assert.equal(new URL(redirect.headers.get('location'), origin).pathname, '/docs/');
  const assets = new Set(['/brand/weknora-original.png', '/product/wiki-browser.png', '/docs/favicon.ico']);
  for (const path of ['/', '/docs/', '/docs/03-features/14-wiki', '/docs/03-features/14-wiki.html']) {
    const response = await fetch(origin + path);
    assert.equal(response.status, 200, path);
    assert.match(response.headers.get('content-type'), /text\/html/);
    assert.equal(response.headers.get('x-content-type-options'), 'nosniff');
    assert.equal(response.headers.get('x-frame-options'), 'SAMEORIGIN');
    assert.equal(response.headers.get('referrer-policy'), 'strict-origin-when-cross-origin');
    const html = await response.text();
    assert.match(html, /WeKnora/i, path);
    for (const [, asset] of html.matchAll(/(?:src|href)="([^"?#]+\.(?:js|css))"/g)) {
      const url = new URL(asset, origin + path);
      if (url.origin === origin) assets.add(url.pathname);
    }
  }
  assert.ok([...assets].some(path => path.startsWith('/_next/')), 'Homepage scripts are missing');
  assert.ok([...assets].some(path => path.startsWith('/docs/assets/')), 'Documentation scripts are missing');
  for (const asset of assets) {
    const response = await fetch(origin + asset);
    assert.equal(response.status, 200, asset);
    assert.doesNotMatch(response.headers.get('content-type'), /text\/html/, asset);
  }
  const compressed = await fetch(origin + '/', { headers: { 'Accept-Encoding': 'gzip' } });
  assert.equal(compressed.headers.get('content-encoding'), 'gzip');
  for (const path of ['/not-found', '/docs/not-found']) {
    const response = await fetch(origin + path);
    assert.equal(response.status, 404, path);
    assert.equal(response.headers.get('x-content-type-options'), 'nosniff');
  }
  console.log(`Docker site check passed: homepage, docs routes, ${assets.size} assets, redirects, 404s, compression, headers and runtime isolation.`);
} finally {
  spawnSync('docker', ['rm', '-f', name], { stdio: 'ignore' });
}
