import assert from 'node:assert/strict';
import test from 'node:test';
import {
  domPurifyAllowedUriRegexp,
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
