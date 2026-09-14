import fs from 'node:fs/promises';
import { constants } from 'node:fs';
import path from 'node:path';
import {
  isWithin, parseFixtureJSON, requireSafe, resolveRuntimePaths,
  validateFixtureConfig, validatePathInput,
} from './guided-learning-config.mjs';

const MAX_FIXTURE_BYTES = 1024 * 1024;

export function validateOwnedEntry(info, uid, { directory = false, privateFile = false } = {}) {
  requireSafe(!info.isSymbolicLink() && (directory ? info.isDirectory() : info.isFile())
    && info.uid === uid && (info.mode & 0o022) === 0,
  'Runtime entries must be owned, non-symlink files/directories without foreign write access');
  if (!directory) {
    requireSafe(info.nlink === 1 && info.size <= MAX_FIXTURE_BYTES,
      'Fixture files must be bounded regular files without hard links');
  }
  if (privateFile) requireSafe((info.mode & 0o7777) === 0o600, 'Seed accounts must have mode 0600');
}

async function ownedDirectories(paths, target, uid) {
  requireSafe(isWithin(paths.runtimeRoot, target, true), 'Directory is outside the chosen checkout runtime');
  const parts = path.relative(paths.runtimeRoot, target).split(path.sep).filter(Boolean);
  let current = paths.runtimeRoot;
  for (const part of ['', ...parts]) {
    current = path.join(current, part);
    validateOwnedEntry(await fs.lstat(current), uid, { directory: true });
    requireSafe(await fs.realpath(current) === current, 'Runtime directory symlinks are forbidden');
  }
}

async function readFixture(paths, file, uid, privateFile = false) {
  await ownedDirectories(paths, path.dirname(file), uid);
  const handle = await fs.open(file, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const info = await handle.stat();
    validateOwnedEntry(info, uid, { privateFile });
    const bytes = Buffer.alloc(MAX_FIXTURE_BYTES + 1);
    let length = 0;
    while (length < bytes.length) {
      const result = await handle.read(bytes, length, bytes.length - length, null);
      if (!result.bytesRead) break;
      length += result.bytesRead;
    }
    requireSafe(length <= MAX_FIXTURE_BYTES, 'Fixture file exceeds size limit');
    await ownedDirectories(paths, path.dirname(file), uid);
    const current = await fs.lstat(file);
    validateOwnedEntry(current, uid, { privateFile });
    requireSafe(current.dev === info.dev && current.ino === info.ino, 'Fixture file changed during read');
    return bytes.subarray(0, length).toString('utf8');
  } finally { await handle.close(); }
}

async function canonicalEnvironment(root, env) {
  const result = { ...env };
  // Permit an OS-level alias of the checkout prefix (e.g. /home -> /data00),
  // but never resolve away symlinks or traversal inside .runtime itself.
  for (const key of ['GL_RUNTIME_DIR', 'GL_EVIDENCE_DIR']) {
    if (env[key] === undefined) continue;
    validatePathInput(env[key]);
    if (!path.isAbsolute(env[key])) continue;
    const parts = path.resolve(env[key]).split(path.sep);
    const marker = parts.indexOf('.runtime');
    requireSafe(marker > 0, 'Absolute runtime paths must name this checkout .runtime');
    const prefix = parts.slice(0, marker).join(path.sep) || path.parse(root).root;
    const info = await fs.lstat(prefix);
    requireSafe(info.isDirectory() && !info.isSymbolicLink() && await fs.realpath(prefix) === root,
      'Absolute runtime paths must name this checkout, not another repository');
    result[key] = path.join(root, ...parts.slice(marker));
  }
  return result;
}

export async function loadFixtureRuntime(repo, env = {}, runName = 'guided-learning') {
  try {
    const root = await fs.realpath(repo);
    const paths = resolveRuntimePaths(root, await canonicalEnvironment(root, env), runName);
    const uid = process.getuid();
    const state = parseFixtureJSON(await readFixture(paths, paths.stateFile, uid, true));
    const manifestText = await readFixture(paths, paths.manifestFile, uid);
    const snapshot = parseFixtureJSON(manifestText);
    const config = validateFixtureConfig(state, snapshot, env);
    await ownedDirectories(paths, path.dirname(paths.evidenceDir), uid);
    try {
      await fs.lstat(paths.evidenceDir);
    } catch (error) {
      if (error.code === 'ENOENT') return { ...config, paths, manifestText };
      throw error;
    }
    throw new Error('Evidence destination already exists');
  } catch {
    throw new Error('Fixture configuration refused: check runtime paths, ownership, 0600 accounts, origins and matching seed manifest (details suppressed)');
  }
}

export async function createEvidenceDirectory(paths) {
  try {
    await ownedDirectories(paths, path.dirname(paths.evidenceDir), process.getuid());
    // mkdir without recursive mode refuses any preexisting file, link or run.
    await fs.mkdir(paths.evidenceDir, { mode: 0o700 });
    await ownedDirectories(paths, paths.evidenceDir, process.getuid());
  } catch { throw new Error('Cannot create a new owned evidence directory (details suppressed)'); }
}

export async function readFixtureManifest(paths) {
  try { return await readFixture(paths, paths.manifestFile, process.getuid()); }
  catch { throw new Error('Cannot reread the owned fixture manifest (details suppressed)'); }
}
