import assert from 'node:assert/strict';
import test from 'node:test';
import {
  resolveAgentWebSearchProviderId,
  isAgentWebSearchReady,
  isTenantWebSearchReady,
} from './agentWebSearch.ts';

const providers = [
  { id: 'p1', name: 'Keenable', is_default: false },
  { id: 'p2', name: 'Default', is_default: true },
];

test('resolveAgentWebSearchProviderId uses explicit agent provider', () => {
  assert.equal(
    resolveAgentWebSearchProviderId({ web_search_provider_id: 'p1' }, providers),
    'p1',
  );
});

test('resolveAgentWebSearchProviderId falls back to tenant default', () => {
  assert.equal(
    resolveAgentWebSearchProviderId({ web_search_provider_id: '' }, providers),
    'p2',
  );
});

test('resolveAgentWebSearchProviderId returns null when default missing', () => {
  const noDefault = [{ id: 'p1', name: 'Keenable', is_default: false }];
  assert.equal(
    resolveAgentWebSearchProviderId({ web_search_provider_id: '' }, noDefault),
    null,
  );
});

test('isAgentWebSearchReady requires enabled flag and resolvable provider', () => {
  assert.equal(
    isAgentWebSearchReady({ web_search_enabled: true }, providers),
    true,
  );
  assert.equal(
    isAgentWebSearchReady({ web_search_enabled: true, web_search_provider_id: '' }, [
      { id: 'p1', name: 'Keenable', is_default: false },
    ]),
    false,
  );
  assert.equal(
    isAgentWebSearchReady({ web_search_enabled: false }, providers),
    false,
  );
});

test('isTenantWebSearchReady checks default provider only', () => {
  assert.equal(isTenantWebSearchReady(providers), true);
  assert.equal(
    isTenantWebSearchReady([{ id: 'p1', name: 'Keenable', is_default: false }]),
    false,
  );
});
