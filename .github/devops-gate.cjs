'use strict';

// Read-only PR metadata and existing-workflow aggregation; never publishes or deploys.
function validatePr(pr) {
  const errors = [];
  // Dependabot's generated release notes do not use our manual PR template.
  if (pr.user?.login === 'dependabot[bot]' && pr.user?.type === 'Bot') {
    if (!/^(build|chore)(\([a-z0-9._/-]+\))?: .+/.test(pr.title || '') || !(pr.body || '').trim()) {
      errors.push('Dependabot PRs still require a conventional dependency title and nonempty release notes.');
    }
    return errors;
  }
  if (
    !/^(feat|fix|docs|refactor|test|perf|build|ci|chore|revert)(\([a-z0-9][a-z0-9._/-]*\))?!?: [a-z][^\n]*$/.test(
      pr.title || '',
    )
  ) {
    errors.push('Title must use type(scope): summary with an English lowercase summary.');
  }
  const body = (pr.body || '').replace(/<!--[\s\S]*?-->/g, '').replace(/\r/g, '');
  const sections = [...body.matchAll(/^##\s+([^\n]+)\n([\s\S]*?)(?=^#{1,2}\s|$(?![\s\S]))/gm)];
  for (const names of [
    ['改动', 'What changed', 'Summary', 'Changes'],
    ['原因', 'Why', 'Reason'],
    ['验证', 'Validation', 'Testing'],
  ]) {
    const section = sections.find((match) =>
      names.some((name) => match[1].trim().toLowerCase() === name.toLowerCase()),
    );
    const content = section?.[2].replace(/^[\s\-*]+$/gm, '').trim();
    if (!content || /^(?:todo|tbd|n\/a|待填写|待补充|\.\.\.)$/i.test(content))
      errors.push(`Fill the ${names[0]} section.`);
  }
  if (/^fix(?:\(|!?\s*:)/.test(pr.title || '')) {
    for (const [label, pattern] of [
      ['trigger', /触发|复现|reproduc|trigger/i],
      ['cause', /根因|原因|root cause|cause/i],
      ['before/after behavior', /修复前|修复后|before|after/i],
    ])
      if (!pattern.test(body.replace(/^#{1,6}\s+.*$/gm, ''))) errors.push(`Bug fixes must explain ${label}.`);
  }
  return errors;
}

function globMatches(path, pattern) {
  let source = '^';
  for (let i = 0; i < pattern.length; i++) {
    const c = pattern[i];
    if (c === '*' && pattern[i + 1] === '*') {
      i++;
      if (pattern[i + 1] === '/') {
        i++;
        source += '(?:.*/)?';
      } else source += '.*';
    } else if (c === '*') source += '[^/]*';
    else if (c === '?') source += '[^/]';
    else source += c.replace(/[\\^$+?.()|{}[\]]/g, '\\$&');
  }
  return new RegExp(`${source}$`).test(path);
}

function workflowApplies(workflow, files, branch) {
  const matches = (value, patterns) =>
    patterns.reduce((included, pattern) => {
      const negative = pattern.startsWith('!');
      return globMatches(value, negative ? pattern.slice(1) : pattern) ? !negative : included;
    }, false);
  if (workflow.branches && !matches(branch, workflow.branches)) return false;
  if (workflow.branchesIgnore && matches(branch, workflow.branchesIgnore)) return false;
  if (workflow.paths) return files.some((file) => matches(file, workflow.paths));
  if (workflow.pathsIgnore) return files.some((file) => !matches(file, workflow.pathsIgnore));
  return true;
}

function evaluateRuns(workflows, runs, sha) {
  const waiting = [],
    failed = [];
  for (const workflow of workflows) {
    const latest = runs
      .filter(
        (run) => run.head_sha === sha && run.event === 'pull_request' && run.path?.split('@')[0] === workflow.path,
      )
      .sort((a, b) => b.id - a.id || b.run_attempt - a.run_attempt)[0];
    if (latest?.status !== 'completed') waiting.push(workflow.name);
    else if (latest.conclusion !== 'success') failed.push(`${workflow.name}: ${latest.conclusion}`);
  }
  return { waiting, failed };
}

async function run({
  github,
  context,
  core,
  config,
  sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
}) {
  const pr = context.payload.pull_request;
  if (!pr) throw new Error('This gate only runs for pull requests.');
  const errors = validatePr(pr);
  if (errors.length) throw new Error(errors.join('\n'));
  const files = await github.paginate(github.rest.pulls.listFiles, {
    ...context.repo,
    pull_number: pr.number,
    per_page: 100,
  });
  // GitHub returns at most 3000 changed files; fail closed rather than miss a filtered workflow.
  if (files.length !== pr.changed_files)
    throw new Error('Incomplete PR file list; split the PR or inspect the API limit.');
  const expected = config.workflows.filter((workflow) =>
    workflowApplies(
      workflow,
      files.map((file) => file.filename),
      pr.base.ref,
    ),
  );
  core.info(
    `Required existing workflows: ${expected.map((workflow) => workflow.name).join(', ') || 'none for this diff'}`,
  );
  for (let attempt = 0; attempt < 90; attempt++) {
    const current = (await github.rest.pulls.get({ ...context.repo, pull_number: pr.number })).data;
    if (current.head.sha !== pr.head.sha || current.state !== 'open')
      throw new Error('PR head changed or PR closed; use the newest gate run.');
    const runs = await github.paginate(github.rest.actions.listWorkflowRunsForRepo, {
      ...context.repo,
      event: 'pull_request',
      head_sha: pr.head.sha,
      per_page: 100,
    });
    const { waiting, failed } = evaluateRuns(expected, runs, pr.head.sha);
    if (failed.length) throw new Error(failed.join('\n'));
    if (!waiting.length) {
      core.info('PR metadata and all applicable CI workflows passed.');
      return;
    }
    if (attempt % 6 === 0) core.info(`Waiting for: ${waiting.join(', ')}`);
    await sleep(20000);
  }
  throw new Error(
    'CI did not finish within 30 minutes. Rerun this gate after fixing or completing the required workflows.',
  );
}

module.exports = { validatePr, globMatches, workflowApplies, evaluateRuns, run };
