Status: DONE

Summary of changes:
- Added WeCom Chat Archive connector labels and field help text in zh-CN and en-US.
- Added English fallback labels for WeCom Chat Archive in ko-KR and ru-RU.
- Added the `wecom_chat_archive` frontend connector definition with required credential fields and docs/permission URLs.
- Applied the specified safe defaults when selecting the WeCom Chat Archive connector.
- Added fallback icon text `企` for `wecom_chat_archive` without adding an icon asset.

Tests/checks run and outputs summarized:
- `npm --prefix frontend run type-check` initially failed because `vue-tsc` was not installed in the worktree (`sh: vue-tsc: command not found`).
- `npm --prefix frontend ci` installed frontend dependencies; npm reported 7 audit vulnerabilities (1 low, 2 moderate, 3 high, 1 critical), not changed by this task.
- `npm --prefix frontend run type-check` passed with `vue-tsc --build` and exit code 0.
- `git diff --check` passed with no whitespace errors before commit.

Commit hash(es):
- c5a4f2f5 feat(frontend): add wecom chat archive datasource option

Self-review notes and concerns:
- No backend files were modified.
- No datasource icon asset was added; the connector uses fallback text as required.
- No component-specific frontend unit test existed for this connector wizard path, so validation used the prescribed type-check command.
- The commit output noted auto-configured git committer identity; no action taken because amending was not requested.
- The frontend dependency install surfaced existing npm audit findings; no dependency changes were made by this task.

---

Review fix status: DONE

Summary of fix changes:
- Extracted datasource selection defaults into `frontend/src/views/knowledge/settings/datasourceDefaults.ts`.
- Updated `selectType()` to always apply connector-specific or normal defaults, so leaving WeCom for any non-WeCom connector clears WeCom resource IDs, selected IDs, settings, sync schedule, sync mode, conflict strategy, and sync deletions.
- Kept RSS behavior intact by preserving empty normal settings for new RSS selections and leaving existing RSS hydration/edit behavior unchanged.
- Added focused frontend unit coverage for WeCom defaults and non-WeCom reset defaults.

TDD evidence:
- `npm --prefix frontend test -- src/views/knowledge/settings/datasourceDefaults.test.ts` initially failed on `returns WeCom archive defaults when selecting WeCom chat archive`, proving the new helper test was red before implementation.
- After implementation, the same test command passed with 2 tests passing.

Tests/checks run and outputs summarized:
- `npm --prefix frontend test -- src/views/knowledge/settings/datasourceDefaults.test.ts` passed: 2 tests, 0 failures.
- `npm --prefix frontend run type-check` passed with `vue-tsc --build` and exit code 0.

Commit hash(es):
- 8ba39912 fix(frontend): reset datasource defaults on connector switch

Self-review notes and concerns:
- No backend files were modified.
- No datasource icon asset was added.
