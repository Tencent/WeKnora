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

const portName = `${name}-port`;
const bashName = `${name}-bash`;
const argsName = `${name}-args`;

async function waitReady(origin, label) {
  for (let attempt = 0; attempt < 30; attempt++) {
    try {
      if ((await fetch(origin, { signal: AbortSignal.timeout(2000) })).ok) return;
    } catch { /* Nginx may still be starting. */ }
    await setTimeout(200);
  }
  assert.fail(`${label} did not become ready`);
}

try {
  docker('run', '-d', '--name', name, '-e', 'PORT=9999', '-p', '127.0.0.1::80', image);
  const origin = `http://${docker('port', name, '80/tcp')}`;
  docker('exec', name, 'nginx', '-t');
  docker('exec', name, 'sh', '-c', '! command -v node && test ! -d /build && test ! -e /usr/share/nginx/html/package.json');
  await waitReady(origin, 'Container');

  // An unrelated PORT must not affect the default or overridden listener.
  // WEBSITE_NGINX_PORT moves the listen port without a rebuild; the default stays 80.
  docker('run', '-d', '--name', portName, '-e', 'WEBSITE_NGINX_PORT=8080', '-e', 'PORT=9999', '-p', '127.0.0.1::8080', image);
  const portOrigin = `http://${docker('port', portName, '8080/tcp')}`;
  await waitReady(portOrigin, 'Container with WEBSITE_NGINX_PORT=8080');
  assert.match(docker('exec', portName, 'cat', '/etc/nginx/conf.d/default.conf'), /listen 8080;/);
  assert.equal((await fetch(portOrigin + '/docs/')).status, 200);

  // Deployment platforms may replace the image entrypoint with /bin/bash -c.
  // The wrapped command must still render the Nginx template before serving.
  docker('run', '-d', '--name', bashName, '--entrypoint', '/bin/bash',
    '-e', 'WEBSITE_NGINX_PORT=8088', '-e', 'uri=must-not-replace-nginx-uri',
    '-p', '127.0.0.1::8088', image, '-c', 'exec /docker-entrypoint.sh');
  const bashOrigin = `http://${docker('port', bashName, '8088/tcp')}`;
  await waitReady(bashOrigin, 'Container launched through Bash');
  docker('exec', bashName, 'nginx', '-t');
  assert.match(docker('exec', bashName, 'cat', '/etc/nginx/conf.d/default.conf'), /listen 8088;/);
  assert.match(docker('exec', bashName, 'cat', '/etc/nginx/conf.d/default.conf'), /try_files \$uri \$uri\.html/);
  assert.equal((await fetch(bashOrigin + '/docs/')).status, 200);
  assert.equal((await fetch(bashOrigin + '/docs/03-features/14-wiki')).status, 200);

  // Like frontend, our entrypoint always configures and runs Nginx, even when
  // the platform passes a shell command as image arguments instead of replacing it.
  docker('run', '-d', '--name', argsName, '-e', 'WEBSITE_NGINX_PORT=8088',
    '-p', '127.0.0.1::8088', image, '/bin/bash', '-c', 'nginx; sleep infinity');
  const argsOrigin = `http://${docker('port', argsName, '8088/tcp')}`;
  await waitReady(argsOrigin, 'Container with platform command arguments');
  assert.equal((await fetch(argsOrigin + '/docs/')).status, 200);
  assert.match(docker('exec', argsName, 'cat', '/proc/1/comm'), /^nginx$/);

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
  console.log(`Docker site check passed: homepage, docs routes, ${assets.size} assets, redirects, 404s, compression, headers, runtime isolation, WEBSITE_NGINX_PORT override and Bash startup.`);
} finally {
  spawnSync('docker', ['rm', '-f', name, portName, bashName, argsName], { stdio: 'ignore' });
}
