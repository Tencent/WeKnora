import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { readScreenshotSizes } from '../.vitepress/screenshot-sizes.mjs';

test('reserves each screenshot intrinsic ratio and picks up replacement dimensions', () => {
  const dir = mkdtempSync(join(tmpdir(), 'weknora-shot-sizes-'));
  try {
    // IHDR bytes from a PNG header; the extractor deliberately does not decode pixels.
    const header = Buffer.from('89504e470d0a1a0a0000000d4948445200000f0000000840', 'hex');
    writeFileSync(join(dir, 'wide.png'), header);
    writeFileSync(join(dir, 'README.md'), 'Not an image');
    assert.deepEqual(readScreenshotSizes(dir), { '/screenshots/wide.png': { width: 3840, height: 2112 } });
    header.writeUInt32BE(900, 16);
    header.writeUInt32BE(1600, 20);
    writeFileSync(join(dir, 'wide.png'), header);
    assert.deepEqual(readScreenshotSizes(dir), { '/screenshots/wide.png': { width: 900, height: 1600 } });
    writeFileSync(join(dir, 'broken.png'), 'truncated');
    assert.throws(() => readScreenshotSizes(dir), /Invalid PNG screenshot/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
