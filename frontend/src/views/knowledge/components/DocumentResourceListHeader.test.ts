import assert from 'node:assert/strict';
import test from 'node:test';

import enUS from '../../../i18n/locales/en-US';
import koKR from '../../../i18n/locales/ko-KR';
import ruRU from '../../../i18n/locales/ru-RU';
import zhCN from '../../../i18n/locales/zh-CN';

test('mixed document and folder lists use a resource-neutral name header', () => {
  assert.equal(zhCN.knowledgeBase.columnName, '名称');
  assert.equal(enUS.knowledgeBase.columnName, 'Name');
  assert.equal(koKR.knowledgeBase.columnName, '이름');
  assert.equal(ruRU.knowledgeBase.columnName, 'Имя');
});
