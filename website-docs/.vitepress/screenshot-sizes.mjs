import { closeSync, openSync, readSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

// Read intrinsic sizes before rendering HTML so anchor positions do not depend
// on when the screenshot requests finish. Product screenshots are PNG files.
export function readScreenshotSizes(directory) {
  const sizes = {};
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    if (!entry.isFile() || !entry.name.endsWith('.png')) continue;
    const file = openSync(join(directory, entry.name), 'r');
    const header = Buffer.alloc(24);
    try {
      if (readSync(file, header, 0, 24, 0) !== 24
        || !header.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))
        || header.toString('ascii', 12, 16) !== 'IHDR') {
        throw new Error(`Invalid PNG screenshot: ${entry.name}`);
      }
      const width = header.readUInt32BE(16);
      const height = header.readUInt32BE(20);
      if (!width || !height) throw new Error(`Invalid screenshot dimensions: ${entry.name}`);
      sizes[`/screenshots/${entry.name}`] = { width, height };
    } finally {
      closeSync(file);
    }
  }
  return sizes;
}
