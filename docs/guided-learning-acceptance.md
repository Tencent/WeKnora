## Guided Learning Acceptance

The scoped v1 is implemented on `feat/guided-learning`, based on `3e6010e7`. It includes personal consent, Wiki topic history and graph state, evidence-backed practice, BKT estimates, recommendations, three Agent tools, export and deletion. The isolated development environment remains running; human learning gains and production-scale performance are not established.

Design: [guided-learning.md](guided-learning.md). User workflow: [feature guide](../website-docs/03-features/22-guided-learning.md). Algorithms and dataset limitations: [evaluation](guided-learning-evaluation.md).

### Verification

Recorded on 2026-09-09 local time; evidence filenames use UTC. Logs below are relative to the ignored `.runtime/` directory in this worktree.

| Layer | Result | Evidence |
| --- | --- | --- |
| Go repository suite | `go test -p 8 ./...` passed | `logs/go-test-all.log` |
| Targeted race tests | Types, learning service, repository, tools, chat models, middleware, handlers, router and container passed | `logs/learning-race-final.log` |
| Real PostgreSQL and SQLite | Learning transactions and Wiki rename tests passed with `-race`; PostgreSQL cases used isolated temporary schemas | `logs/learning-postgres-final.log` |
| Static analysis | `go vet` passed for the same changed backend packages | Command below |
| Frontend | 693 tests passed; zero failures/skips; TypeScript check passed | `logs/frontend-test.log`, `logs/frontend-typecheck.log` |
| Production frontend build | Passed in 37.75 seconds; existing large-chunk warnings remain | `logs/frontend-build.log` |
| Real HTTP and Agent | 36 checks passed using DeepSeek; 15 observations counted separately | `evidence/api-acceptance-20260908T232006-57726.json` |
| Browser final replay | 11 passed, 3 skipped; Chromium 145, desktop 1280x900 and mobile 390x844 | `logs/browser-delivery.tap` |
| Runtime | API, frontend proxy, PostgreSQL, Redis and Qdrant checks passed; migration 92 clean, five learning tables | `evidence/application-smoke.json`, `evidence/middleware-smoke.json` |
| Synthetic algorithms | Eight ranking cases, nine state snapshots and eight answer traces passed their arithmetic/state checks | `logs/learning-offline-final.log` |

The HTTP test generates a real three-question quiz, verifies pre-answer key omission, submits and retries an answer, checks conflicting submissions and cross-tenant access, then exports and deletes the disposable learner_b profile. It executes all three learning tools through `/agent-chat`; successful tool results, not repeated SSE progress events, establish execution. The model configured for these fixtures is `deepseek-v4-flash`; the quiz prompt is `learning-quiz-v1`.

Final source-deletion regression tests cover soft/hard deletion, tenant movement, an unquoted generation source, a surviving multi-source page, bounded recovery after opt-out, preservation of a newer assessment, and export racing a PostgreSQL deletion. Quizzes retain source-document IDs independently of current Wiki references. Export purges unavailable-source evidence under the profile/source locks; recovery performs the same cleanup without waiting for an export. Ordinary source edits retain historical answers while making the quiz stale.

The final browser run replays a previously generated and answered real quiz and a persisted Agent card. It includes a separately labelled mock-503 recovery case. It does not generate a new quiz or resubmit saved answers. The three skips are incomplete-source rejection because sources are now valid, duplicate standalone export, and destructive learner_b clearing. An earlier browser clear-confirmation run passed against an empty learner_b history; deletion of a nonempty history is established by the real HTTP test, not that UI run.

Browser evidence: `evidence/guided-learning-2026-09-08T23-21-24-535Z-61307/`. Source hashes were unchanged during that run. The earlier clear run is `evidence/guided-learning-2026-09-08T22-23-38-440Z-4123598/`. Screenshots mask account identity; exports still contain synthetic practice records and should remain private.

### Reproduce

Use the Go toolchain from `go.mod` and install frontend dependencies with the repository lockfile. Run these from the worktree root:

```bash
go test -p 8 ./...
go test -race ./internal/types ./internal/application/service/learning \
  ./internal/application/repository ./internal/agent/tools \
  ./internal/models/chat ./internal/middleware ./internal/handler \
  ./internal/router ./internal/container
go vet ./internal/types ./internal/application/service/learning \
  ./internal/application/repository ./internal/agent/tools \
  ./internal/models/chat ./internal/middleware ./internal/handler \
  ./internal/router ./internal/container
npm --prefix frontend test
npm --prefix frontend run type-check
npm --prefix frontend run build
go test ./internal/application/service/learning \
  -run TestLearningOfflineEvaluation -v -count=1
```

For this retained development database only, credentials stay in environment variables:

```bash
set +x
source .runtime/secrets.env
export LEARNING_TEST_POSTGRES_DSN="host=127.0.0.1 port=25432 user=weknora-gl password=$DB_PASSWORD dbname=weknora-gl sslmode=disable"
export WEKNORA_WIKI_RENAME_TEST_DSN="$LEARNING_TEST_POSTGRES_DSN"
go test -race ./internal/application/repository \
  -run 'Learning|Wiki.*Rename|Rename.*Wiki' -count=1 -v
```

Live HTTP acceptance explicitly opts in and clears only the disposable learner_b profile. Do not run it concurrently with browser privacy tests:

```bash
python3 -B scripts/test-guided-learning-api.py inspect
python3 -B scripts/test-guided-learning-api.py run --consent-learner-b
```

The browser replay uses the retained learner_a fixtures. Set `PLAYWRIGHT_MODULE_PATH` to an installed Playwright package with its Chromium binary:

```bash
GL_SINGLE_PROCESS=1 GL_RUN_QUIZ=1 GL_REPLAY_QUIZ=1 \
GL_EXISTING_QUIZ_ID=8203a58e-8761-46d6-9784-81ff78cad9c8 \
GL_CARD_SESSION_ID=8959ba30-9dc6-408f-a83c-d0955168b1c8 \
GL_RUN_MOCK_ERRORS=1 GL_RUN_LEARNER_B_CHECKS=1 \
PLAYWRIGHT_MODULE_PATH=/home/liudebao/opensource/WeKnora-sandbox-workbench/.runtime/node_modules/playwright \
node --test frontend/e2e/guided-learning.spec.mjs
```

These IDs belong only to the retained fixture. Reparse/reseed can replace page and chunk identities; consult `.runtime/evidence/seed.json` before another run. `GL_SINGLE_PROCESS=1` accommodates this host's browser sandbox restrictions. Earlier export runs emitted Chromium shutdown warnings despite passing assertions; no browser-process isolation claim follows from this mode.

### Retained Demo

Worktree: `/home/liudebao/opensource/WeKnora-guided-learning`. Frontend: `http://10.37.40.48:25173/` or `http://127.0.0.1:25173` through IDE forwarding. API and middleware bind only to loopback. This Vite server is not intended for public Internet exposure.

The private `.runtime/seed-accounts.json` contains the two disposable login accounts. learner_a retains consent, six saved answers, two quizzes and a replayable tool-card conversation. Its built-in Guided Learning Agent is bound to its existing model and Wiki KB. learner_b remains disabled and empty after acceptance. No real-user account or sibling environment is used.

```bash
bash .runtime/runtime.sh status
bash .runtime/runtime.sh start
bash .runtime/services.sh api-rebuild
python3 .runtime/verify-app.py
# Stop only this runtime; preserve database volumes and credentials:
bash .runtime/runtime.sh stop
```

The private `.runtime/README.md` documents setup, process ownership, seeding and credential handling. `.runtime/configure-demo.py` idempotently restores the learner_a Agent binding without changing consent. Keep `.runtime/` out of commits and Docker build contexts. SQL, JWT, encryption and model secrets are held in owner-only files; no values belong in public reports.

### Limits

- V1 admits authenticated Web users with workspace-owned Wiki KBs. Shared content/Agents, API keys, IM and Embed are excluded. Long course plans and inferred prerequisite graphs are deferred.
- BKT parameters are uncalibrated. On the assistant-authored synthetic dataset, BKT Brier is 0.277601 versus 0.264400 for fixed prior; lower is better. Ranking NDCG@5 is 0.813841 after a development-set correction, so this is not an untouched heldout result.
- Exact source quotes and same-model blinded agreement do not prove semantic correctness or teaching quality. There is no participant study or measured human learning gain.
- Wiki rename locks the KB's live pages to preserve UUID and references. Changed retrieval projections may require reindexing and report a warning; large-KB rename latency was not measured.
- Per-profile bounds are 2,000 node records, 1,000 quizzes and four pending/running quizzes. Full production load, cross-provider behavior and large-profile export latency are unmeasured.
- Clearing one KB preserves other KB history but advances the profile epoch, making their active/ready quizzes stale. This prevents delayed publication across a privacy action.
- Final integration checks covered identity admission, source freshness, epoch/lease fencing, idempotency, logging and deletion. An independent risk check identified the source-deletion gap fixed above; a complete independent sign-off was not obtained.
