import assert from 'node:assert/strict';
import test from 'node:test';
import {
  domPurifyAllowedUriRegexp,
  domPurifySecurityHooks,
  markdownDomPurifyConfig,
  markdownDomPurifySecurityHooks,
} from './markdownDomPurify.ts';

test('markdownDomPurifyConfig FORBID_TAGS includes script', () => {
  assert.ok(Array.isArray(markdownDomPurifyConfig.FORBID_TAGS));
  assert.ok(markdownDomPurifyConfig.FORBID_TAGS.includes('script'));
});

test('ALLOWED_URI_REGEXP allows s3:// and rejects javascript:', () => {
  const re = markdownDomPurifyConfig.ALLOWED_URI_REGEXP ?? domPurifyAllowedUriRegexp;
  assert.match('s3://bucket/key', re);
  assert.doesNotMatch('javascript:alert(1)', re);
});

test('markdownDomPurifyConfig keeps hook-added target attribute', () => {
  // USE_PROFILES replaces ALLOWED_ATTR, so target must come from ADD_ATTR.
  assert.ok(markdownDomPurifyConfig.ADD_ATTR.includes('target'));
});

test('author-supplied target never survives; only the hook adds _blank with noopener', () => {
  // ADD_ATTR allows target globally, so the before hook must drop any
  // author value. SVG <a> (lowercase tagName), <area> and non-"http" hrefs
  // never reach the after hook's rewrite and would otherwise keep it.
  const cases = [
    { tagName: 'a', href: 'https://evil.example' },
    { tagName: 'AREA', href: 'https://evil.example' },
    { tagName: 'A', href: '//evil.example' },
    { tagName: 'A', href: 'HTTPS://evil.example' },
    { tagName: 'A', href: ' https://evil.example' },
    { tagName: 'A', href: '/relative' },
    { tagName: 'DIV', href: null },
  ];
  const hookSets = {
    base: domPurifySecurityHooks,
    markdown: markdownDomPurifySecurityHooks,
  };
  for (const [name, hooks] of Object.entries(hookSets)) {
    for (const { tagName, href } of cases) {
      const attributes = new Map([['target', '_top'], ['rel', 'opener']]);
      if (href !== null) attributes.set('href', href);
      const el = {
        tagName,
        getAttribute: (n) => attributes.get(n) ?? null,
        setAttribute: (n, v) => attributes.set(n, v),
        hasAttribute: (n) => attributes.has(n),
        removeAttribute: (n) => attributes.delete(n),
      };
      hooks.beforeSanitizeElements(el);
      hooks.afterSanitizeElements(el);
      const label = `${name} ${tagName} ${JSON.stringify(href)}`;
      if (attributes.has('target')) {
        assert.equal(attributes.get('target'), '_blank', label);
        assert.equal(attributes.get('rel'), 'noopener noreferrer', label);
      }
    }
  }
});

test('chat markdown links always open in a new tab', () => {
  const attributes = new Map([['href', '/platform/knowledge-bases/kb-1']]);
  const anchor = {
    tagName: 'A',
    getAttribute: (name) => attributes.get(name) ?? null,
    setAttribute: (name, value) => attributes.set(name, value),
    hasAttribute: (name) => attributes.has(name),
    removeAttribute: (name) => attributes.delete(name),
  };

  markdownDomPurifySecurityHooks.afterSanitizeElements(anchor);

  assert.equal(attributes.get('target'), '_blank');
  assert.equal(attributes.get('rel'), 'noopener noreferrer');
});

test('protected resource download cards stay in the current tab', () => {
  const attributes = new Map([
    ['href', 'blob:http://localhost/file'],
    ['class', 'protected-resource-card'],
    ['download', 'deck.pptx'],
    ['target', '_blank'],
  ]);
  const anchor = {
    tagName: 'A',
    getAttribute: (name) => attributes.get(name) ?? null,
    setAttribute: (name, value) => attributes.set(name, value),
    hasAttribute: (name) => attributes.has(name),
    removeAttribute: (name) => attributes.delete(name),
  };

  markdownDomPurifySecurityHooks.afterSanitizeElements(anchor);

  assert.equal(attributes.has('target'), false);
});
