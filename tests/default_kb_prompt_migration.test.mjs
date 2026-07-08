import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const promptTemplate = readFileSync(resolve(root, 'config/prompt_templates/system_prompt.yaml'), 'utf8');
const migration = readFileSync(
  resolve(root, 'migrations/versioned/000065_builtin_quick_answer_default_prompt.up.sql'),
  'utf8',
);

const requiredPromptRules = [
  '<kb doc="..." chunk_id="..." kb_id="..." />',
  'Retrieved information may contain images in either of these formats',
  'Prefer image-rich answers whenever possible',
  '"鉴别", "区别", "表现", "眼底表现", "长什么样", "图片", "图", "照片", "展示"',
  'Do not omit relevant images merely because the user did not explicitly ask for pictures',
  'Output each image title and image as exactly two consecutive Markdown lines',
  'If the retrieved context contains a description, case note, OCR text, figure explanation, or 图点评',
  '{{contexts}}',
];

for (const rule of requiredPromptRules) {
  assert.ok(promptTemplate.includes(rule), `default_kb prompt is missing rule: ${rule}`);
}

const requiredMigrationRules = [
  'UPDATE custom_agents',
  'builtin-quick-answer',
  'system_prompt',
  'system_prompt_id',
  'default_kb',
  '<kb doc="..." chunk_id="..." kb_id="..." />',
  'Prefer image-rich answers whenever possible',
  'Output each image title and image as exactly two consecutive Markdown lines',
  '{{contexts}}',
];

for (const rule of requiredMigrationRules) {
  assert.ok(migration.includes(rule), `quick-answer prompt migration is missing rule: ${rule}`);
}

assert.ok(
  !migration.includes("config - 'system_prompt'"),
  'quick-answer prompt migration must seed system_prompt, not remove it',
);
