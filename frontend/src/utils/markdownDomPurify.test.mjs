import assert from 'node:assert/strict';
import test from 'node:test';
import {
  domPurifyAllowedUriRegexp,
  markdownDomPurifyConfig,
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
