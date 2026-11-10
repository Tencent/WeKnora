import { copyFileSync, existsSync, readdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// Documentation screenshots are authoritative; retain homepage-only assets.
export function syncProductScreenshots(source, destination) {
  const aliases = { 'sandbox-terminal.png': 'sandbox-panel-terminal.png' };
  let copied = 0;
  for (const entry of readdirSync(destination, { withFileTypes: true })) {
    if (!entry.isFile()) continue;
    const original = join(source, aliases[entry.name] ?? entry.name);
    if (!existsSync(original)) continue;
    copyFileSync(original, join(destination, entry.name));
    copied++;
  }
  return copied;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const homepage = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const copied = syncProductScreenshots(
    resolve(homepage, '../public/screenshots'),
    resolve(homepage, 'public/docs/_home/product'),
  );
  console.log(`Synchronized ${copied} product screenshots from documentation sources.`);
}
