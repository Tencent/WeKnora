import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { syncProductScreenshots } from './sync-product-screenshots.mjs';

test('refreshes changed source images and aliases without replacing homepage-only assets', t => {
  const root = mkdtempSync(join(tmpdir(), 'weknora-shots-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const source = join(root, 'source');
  const destination = join(root, 'product');
  mkdirSync(source);
  mkdirSync(destination);
  writeFileSync(join(source, 'wiki-graph-groups.png'), 'new graph');
  writeFileSync(join(source, 'sandbox-panel-terminal.png'), 'new terminal');
  writeFileSync(join(destination, 'wiki-graph-groups.png'), 'old graph');
  writeFileSync(join(destination, 'sandbox-terminal.png'), 'old terminal');
  writeFileSync(join(destination, 'homepage-only.png'), 'keep this');
  assert.equal(syncProductScreenshots(source, destination), 2);
  assert.equal(readFileSync(join(destination, 'wiki-graph-groups.png'), 'utf8'), 'new graph');
  assert.equal(readFileSync(join(destination, 'sandbox-terminal.png'), 'utf8'), 'new terminal');
  assert.equal(readFileSync(join(destination, 'homepage-only.png'), 'utf8'), 'keep this');
  writeFileSync(join(source, 'wiki-graph-groups.png'), 'updated graph again');
  syncProductScreenshots(source, destination);
  assert.equal(readFileSync(join(destination, 'wiki-graph-groups.png'), 'utf8'), 'updated graph again');
});
