import { stat } from 'node:fs/promises';
import { resolve, sep } from 'node:path';
export async function resolveSiteFile(root, pathname) {
  let decoded;
  try { decoded = decodeURIComponent(pathname); } catch { return null; }
  if (decoded.includes('\0') || decoded.includes('\\')) return null;
  const path = resolve(root, `.${decoded}`);
  if (path !== root && !path.startsWith(root + sep)) return null;
  for (const candidate of [path, `${path}.html`, resolve(path, 'index.html')]) {
    const info = await stat(candidate).catch(() => null);
    if (info?.isFile()) return { path: candidate, info };
  }
  return null;
}
