'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { validatePr, workflowApplies, evaluateRuns, run } = require('./devops-gate.cjs');
const body =
  '## 改动\n保留更新请求未携带的字段。\n## 原因\n触发：只改名。原因：旧实现清空缺席字段。修复前丢配置，修复后保留。\n## 验证\n接口回归测试通过，尚未部署。';
test('valid bug description passes; missing trigger and blank template fail', () => {
  assert.deepEqual(validatePr({ title: 'fix(portal): preserve omitted fields', body }), []);
  assert.ok(validatePr({ title: 'update', body }).length);
  assert.ok(validatePr({ title: 'fix: preserve fields', body: body.replace('触发', '情形') }).length);
  assert.ok(validatePr({ title: 'fix: preserve fields', body: body.replace('原因：旧实现清空缺席字段。', '') }).length);
  assert.ok(
    validatePr({ title: 'docs: update conventions', body: '## 改动\n<!-- done -->\n## 原因\nTBD\n## 验证\n-\n' })
      .length,
  );
});
test('aggregation fails closed for failed CI, changed head and incomplete file lists', async () => {
  const pr = {
    number: 1,
    title: 'ci: enforce gates',
    body,
    changed_files: 1,
    head: { sha: 'abc' },
    base: { ref: 'main' },
  };
  const options = {
    context: { repo: { owner: 'example', repo: 'example' }, payload: { pull_request: pr } },
    core: { info() {} },
    config: { workflows: [{ name: 'CI', path: '.github/workflows/ci.yml' }] },
    sleep: async () => {},
  };
  const github = {
    rest: {
      pulls: { listFiles: 'files', get: async () => ({ data: { head: pr.head, state: 'open' } }) },
      actions: { listWorkflowRunsForRepo: 'runs' },
    },
    paginate: async (endpoint) =>
      endpoint === 'files'
        ? [{ filename: 'main.go' }]
        : [
            {
              id: 1,
              path: '.github/workflows/ci.yml',
              event: 'pull_request',
              head_sha: 'abc',
              status: 'completed',
              conclusion: 'failure',
            },
          ],
  };
  await assert.rejects(run({ ...options, github }), /CI: failure/);
  await assert.rejects(
    run({
      ...options,
      github: {
        ...github,
        rest: {
          ...github.rest,
          pulls: { ...github.rest.pulls, get: async () => ({ data: { head: { sha: 'new' }, state: 'open' } }) },
        },
      },
    }),
    /head changed/,
  );
  await assert.rejects(run({ ...options, github: { ...github, paginate: async () => [] } }), /Incomplete PR file list/);
});
test('paths match root Go files, exclusions and branch selection', () => {
  const workflow = { paths: ['**/*.go', '!cli/**'], branches: ['main'] };
  assert.equal(workflowApplies(workflow, ['main.go'], 'main'), true);
  assert.equal(workflowApplies(workflow, ['cli/main.go'], 'main'), false);
  assert.equal(workflowApplies(workflow, ['internal/server.go'], 'master'), false);
  assert.equal(workflowApplies({ pathsIgnore: ['docs/**'] }, ['docs/a.md'], 'main'), false);
});
test('only the actual Dependabot bot may use generated release notes', () => {
  const pr = {
    title: 'build(deps): Bump dependency',
    body: 'Release notes and dependency comparison.',
    user: { login: 'dependabot[bot]', type: 'Bot' },
  };
  assert.deepEqual(validatePr(pr), []);
  assert.ok(validatePr({ ...pr, user: { login: 'a-person', type: 'User' } }).length);
});
test('missing, cancelled and newest failed CI never pass; other heads cannot satisfy the gate', () => {
  const expected = [{ name: 'CI', path: '.github/workflows/ci.yml' }];
  const success = {
    id: 1,
    run_attempt: 1,
    path: expected[0].path,
    event: 'pull_request',
    head_sha: 'abc',
    status: 'completed',
    conclusion: 'success',
  };
  assert.deepEqual(evaluateRuns(expected, [success], 'abc'), { waiting: [], failed: [] });
  assert.equal(evaluateRuns(expected, [success], 'other').waiting.length, 1);
  assert.equal(evaluateRuns(expected, [success, { ...success, id: 2, conclusion: 'failure' }], 'abc').failed.length, 1);
  assert.equal(evaluateRuns(expected, [{ ...success, conclusion: 'cancelled' }], 'abc').failed.length, 1);
});
