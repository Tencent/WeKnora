import { spawnSync } from 'node:child_process';
import { cpSync, rmSync, existsSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
function run(dir, script) {
  const result = spawnSync('npm', ['run', script], { cwd: resolve(root, dir), stdio: 'inherit' });
  if (result.status !== 0) process.exit(result.status || 1);
}
for (const dir of ['homepage', '.']) {
  if (!existsSync(resolve(root, dir, 'node_modules'))) throw new Error('Dependencies missing. Run npm run setup first.');
}
run('homepage', 'build');
run('.', 'check:docs');
run('.', 'build:docs');
const output = resolve(root, 'homepage/out');
rmSync(resolve(output, 'docs'), { recursive: true, force: true });
cpSync(resolve(root, '.vitepress/dist'), resolve(output, 'docs'), { recursive: true });
run('homepage', 'audit:content');
const check = spawnSync(process.execPath, ['scripts/check-site.mjs', output], { cwd: root, stdio: 'inherit' });
if (check.status !== 0) process.exit(check.status || 1);
// Update the deployment directory only after both applications and their links pass.
const destination = resolve(root, 'static-site');
rmSync(destination, { recursive: true, force: true });
cpSync(output, destination, { recursive: true });
console.log('Unified site ready: static-site/ (homepage + /docs/).');
