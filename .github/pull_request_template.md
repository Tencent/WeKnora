<!-- Title should follow Conventional Commits, e.g. `feat: ...`, `fix: ...`, `docs: ...` -->

## Description
<!-- Briefly describe the purpose and changes of this PR -->

## Type of Change
<!-- Check applicable items -->
- [ ] 🐛 Bug fix
- [ ] ✨ New feature
- [ ] 💥 Breaking change
- [ ] 📚 Documentation update
- [ ] 🎨 Refactor
- [ ] ⚡ Performance improvement
- [ ] 🧪 Test
- [ ] 🔧 Configuration / Build / CI

## Related Issue
<!-- If this PR resolves an issue, use "Fixes #123" or "Closes #123" -->
Fixes #

## Testing
<!-- Describe how these changes were tested. Include reproduction or verification steps. -->
<!--
For a focused change, run checks scoped to the files/packages you changed.
See the Contributing section in README.md for examples. If a full-repository
check is blocked by unrelated baseline or environment failures, record the
exact command and failure here.
-->

## Checklist
- [ ] `git diff --check origin/main...HEAD` passes
- [ ] Changed source files are formatted
- [ ] Targeted tests for the changed packages/components pass
- [ ] Diff-scoped lint passes where applicable (for Go: `golangci-lint run --new-from-rev=origin/main ./...`)
- [ ] Full-repository checks were run, or any unrelated/environment-dependent failures are documented above
- [ ] Self-reviewed the code
- [ ] Added/updated tests covering the change
- [ ] Updated related documentation (README, `docs/`, Swagger annotations, etc.)
- [ ] Breaking changes are clearly called out in the description above

## Screenshots / Recordings
<!-- Required for user-visible UI changes -->
