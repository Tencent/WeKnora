import assert from 'node:assert/strict';
import { readFile, stat } from 'node:fs/promises';
import { join } from 'node:path';

const output = join(process.cwd(), 'out');
const html = await readFile(join(output, 'index.html'), 'utf8');
const visible = html.replace(/<script\b[^>]*>[\s\S]*?<\/script>/g, '').replace(/<[^>]+>/g, '');
assert.equal((html.match(/<h1\b/g) || []).length, 1, 'The homepage must have one main heading');
for (const term of ['帮你找到答案', '并将知识付诸实践', 'v0.8.3', 'RAG', 'Agent', 'Wiki', 'ClawHub', 'SkillHub', 'Docker', 'E2B', 'Cube', '长期记忆', '交互终端', '图形桌面', '本机浏览器', 'BrowserSkill', 'MCP Server', '画廊', '多模态 Embedding', '腾讯 WeMM-Embedding', '内置 30 家厂商', '分叉或回滚', '原文引用定位', '附件按需读取', '平台用户管理', 'Seafile', 'Confluence', '钉钉', 'GitLab', '腾讯 IMA', 'LiteLLM', 'DeepSeek Harness']) {
  assert.ok(visible.includes(term), `Missing product content: ${term}`);
}
for (const term of ['三套', '方案选择', '历史 Demo', '18K+', '成为行动力', '从零散文档', '到鲜活知识']) {
  assert.ok(!visible.includes(term), `Retired content still visible: ${term}`);
}
assert.match(html, /<video[^>]+preload="none"/, 'Video should not load before user interaction');
assert.match(html, /https:\/\/github.com\/user-attachments\/assets\/2819598d-3140-4623-814a-8162a22b653c/, 'Use the README video');
assert.ok(!/<video[^>]+autoplay/i.test(html), 'Do not autoplay the product film');
const wiki = html.match(/<section id="wiki"[\s\S]*?<\/section>/)?.[0];
assert.ok(wiki, 'The homepage must include a dedicated Wiki section');
for (const term of ['/docs/_home/product/wiki-browser.png', '/docs/_home/product/wiki-graph-groups.png', '/docs/_home/product/wiki-revision-history.png', 'id="wiki-gallery"', '/docs/03-features/14-wiki.html', '来源引用', '知识图谱', '主题说明', '返回全图', '版本差异']) {
  assert.ok(wiki.includes(term), `Missing Wiki showcase content: ${term}`);
}
// Release highlights and enduring capabilities must survive independently.
const release = html.match(/<section id="release"[\s\S]*?<\/section>/)?.[0];
for (const term of ['画廊', '多模态 Embedding', '腾讯 WeMM-Embedding', 'Wiki 图谱', '原文引用定位', '附件按需读取']) {
  assert.ok(release?.includes(term), `Missing release highlight: ${term}`);
}
const browser = html.match(/<section id="agent-workflow"[\s\S]*?<\/section>/)?.[0];
assert.ok(browser?.includes('/docs/_home/product/local-browser-task.png') && browser.includes('BrowserSkill'), 'Keep the local browser showcase outside the release section');
for (const asset of ['browser-connection.png', 'browserskill.png']) {
  assert.ok(browser.includes(asset), `Missing browser showcase asset: ${asset}`);
}
const ecosystem = html.match(/<section id="ecosystem"[\s\S]*?<\/section>/)?.[0];
assert.ok(ecosystem?.includes('通过 MCP Server 共享知识') && ecosystem.includes('/docs/03-features/08-mcp.html'), 'Keep MCP Server in the integrations section');
const ids = new Set([...html.matchAll(/\bid="([^"]+)"/g)].map(match => match[1]));
for (const [, anchor] of html.matchAll(/href="#([^"]+)"/g)) {
  assert.ok(ids.has(anchor), `Missing navigation target: #${anchor}`);
}
const assets = new Set([...html.matchAll(/(?:src|href|poster)="(\/[^"?#]*)/g)].map(match => match[1]));
for (const asset of assets) {
  const path = join(output, decodeURIComponent(asset));
  let file = await stat(path);
  if (file.isDirectory()) file = await stat(join(path, 'index.html'));
  assert.ok(file.isFile() && file.size > 0, `Missing local asset: ${asset}`);
}
const pendingShots = [...html.matchAll(/website-docs\/homepage\/public(\/docs\/_home\/product\/[\w.-]+)/g)].map(match => match[1]);
// Gallery slides without a file render their placeholder only once selected; read the flag from the page data.
for (const [, image] of html.matchAll(/\\?"image\\?":\\?"([\w-]+)\\?"[^{}]*?\\?"available\\?":false/g)) pendingShots.push(`/docs/_home/product/${image}.png`);
if (pendingShots.length) console.warn(`Product screenshots still pending (placeholder shown): ${[...new Set(pendingShots)].join(', ')}`);
assert.match(html, /aria-controls="main-navigation"/);
assert.match(html, /aria-label="播放 WeKnora 产品介绍视频/);
console.log(`Homepage audit passed: product content, README video, navigation anchors, ${assets.size} local assets, and retired design choices.`);
