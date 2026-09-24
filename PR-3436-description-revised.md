## Description

Add a personal knowledge map that projects user behavior onto the WeKnora Wiki graph.

- derive a deterministic ten-level mastery state from four observable behavior signals: document citations, effective view duration, answer likes, and neighboring-node spread; no model-generated mastery scores are used;
- identify a user's knowledge boundary with personalized PageRank and support a closed-loop guidance flow: show a candidate node, record the click, and confirm effective viewing;
- expose a personal mastery profile with view, self-contained HTML export, and delete operations, isolated by tenant and user;
- protect all KB-scoped mastery routes with the existing KB read-access check and add a cross-tenant access regression test;
- record citation evidence asynchronously and make citation events idempotent across retries and duplicate task delivery;
- add six dedicated ledgers and dual-database migrations without colliding with upstream: PostgreSQL `000112`–`000115`, SQLite `000032`–`000035`;
- split the implementation into five logical commits to make the feature easier to review.

## Type of Change

- [ ] 🐛 Bug fix
- [x] ✨ New feature
- [ ] 💥 Breaking change
- [ ] 📚 Documentation update
- [ ] 🎨 Refactor
- [x] ⚡ Performance improvement
- [x] 🧪 Test
- [ ] 🔧 Configuration / Build / CI

## Related Issue

N/A — Rhino Bird Topic 4 knowledge network and guided learning.

## Testing

- PASS — Go build for the affected internal packages
- PASS — mastery service and deterministic state calculation tests
- PASS — citation repository and database-level idempotency tests
- PASS — duplicate delivery and retry behavior for citation events
- PASS — migration directory, schema, and upgrade tests
- PASS — router and cross-tenant KB access regression tests
- PASS — repository and type-level tests
- PASS — formatting and diff validation

## Checklist

- [x] KB-scoped mastery routes are protected by `g.KBAccessRead(...)`
- [x] Cross-tenant access regression test added
- [x] Migration numbering rebased and kept contiguous
- [x] Citation recording moved off the synchronous answer path
- [x] Citation events are idempotent across retries and duplicate delivery
- [x] Deterministic mastery and boundary logic covered by tests
- [x] Changed source files are formatted
- [x] Targeted tests pass
- [x] Self-reviewed the code
- [x] Added/updated tests covering the change
- [ ] Updated related documentation
- [ ] Breaking changes are clearly called out

## Screenshots / Recordings

Knowledge guidance view with water-level nodes, neighboring-node highlights, and boundary ripple; personal knowledge profile with self-contained HTML export.


