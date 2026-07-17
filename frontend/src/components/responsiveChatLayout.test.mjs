import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const frontendRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');

const readSource = (relativePath) => readFile(resolve(frontendRoot, relativePath), 'utf8');

test('chat shell can shrink below phone viewport width', async () => {
  const source = await readSource('views/chat/index.vue');

  assert.match(source, /width:\s*100%;\s*[\s\S]{0,120}max-width:\s*calc\(100vw - 260px\);\s*[\s\S]{0,80}min-width:\s*0;/);
  assert.match(source, /@media\s*\(max-width:\s*959px\)[\s\S]{0,220}max-width:\s*100%;/);
});

test('platform shell does not impose a desktop minimum width on mobile', async () => {
  const source = await readSource('views/platform/index.vue');

  assert.match(source, /\.main\s*\{[\s\S]{0,180}min-width:\s*0;/);
});

test('new-chat layout uses container-driven sizing on narrow screens', async () => {
  const source = await readSource('views/creatChat/creatChat.vue');

  assert.doesNotMatch(source, /@media\s*\(max-width:\s*(?:1250|1045|750|600)px\)[\s\S]{0,180}translateX\(-250px\)/);
  assert.match(source, /@media\s*\(max-width:\s*959px\)[\s\S]{0,260}max-width:\s*100%;/);
});

test('knowledge-base chat input does not restore fixed mobile widths', async () => {
  const source = await readSource('views/knowledge/KnowledgeBase.vue');

  assert.doesNotMatch(source, /@media\s*\(max-width:\s*(?:1250|1045|750|600)px\)[\s\S]{0,180}translateX\(-/);
  assert.match(source, /@media\s*\(max-width:\s*959px\)[\s\S]{0,220}width:\s*100% !important;/);
});
