## Guided Learning Evaluation

Topic4 has a reproducible synthetic check of next-topic ranking, response prediction, and learning state. On this fixed set, production ranking beats both baselines in aggregate; BKT response prediction loses to the fixed-prior baseline. **Human learning gains remain unmeasured.**

### Reproduce

From the repository root:

```sh
go test ./internal/application/service/learning -run TestLearningOfflineEvaluation -v -count=1
```

The Go toolchain required by `go.mod` and its module dependencies must already be installed or cached. This test needs no credentials, model, database, Redis, Docker, or network service. With Go 1.26.0 on `PATH`, disable toolchain/module downloads explicitly:

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test ./internal/application/service/learning -run TestLearningOfflineEvaluation -v -count=1
```

The recorded run used the cached Go 1.26.0 binary at `/home/liudebao/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin/go` with those offline settings. The workstation's base `go` is 1.24.4; `GOTOOLCHAIN=local` requires selecting the newer binary first. A fresh machine cannot bootstrap uncached Go dependencies offline.

The command prints every case, selected topic IDs, metrics, sample counts, dataset hash, production algorithm version, and ranking/BKT source hashes. `-count=1` avoids cached test results. Failed arithmetic, state, or fixture guards prevent measurement; failed case checks prevent aggregate reporting. There is no assertion that production must beat a baseline.

### Dataset Identity

The [fixture](../internal/application/service/learning/testdata/offline_eval.json) is `topic4-synthetic-v1`, schema 1. Its raw-byte SHA-256 is pinned by the [test](../internal/application/service/learning/offline_eval_test.go):

Revision on 2026-09-09: changed/missing-source state snapshots now retain historical counts and timestamps while showing `review_due` and the prior probability. Ranking labels, candidate sets and prediction observations are unchanged. The memory stub now exercises the retrieval-conditioned affinity API. The results below were rerun after these corrections.

```text
8bc5d4d5fe393466ba5abd27f74f4c47301a887f88e8d6170d376b6cf3f43237
```

Recorded on 2026-09-09, with fixed evaluation time `2026-09-09T12:00:00Z`:

- 8 ranking scenarios, 12 distinct synthetic topics, 64 candidate occurrences; 8 candidates per scenario.
- 9 state snapshots covering unseen, read-only, uncredited probability, learning, insufficient answers, mastered, due, changed evidence, and missing evidence.
- 8 separate synthetic learner-topic traces, 21 training observations, 24 heldout observations, 12 heldout correct answers. Every trace has three heldout observations; one has no training history.
- Algorithm `bkt-v1`; no model or prompt version applies because no model is called.

Production source SHA-256 values for this measurement:

```text
internal/application/service/learning/recommend.go
524c1d259a9cfb928208ac939614c4be25e30862a64af93dbec03d6ae5ad79a8
internal/types/learning_algorithm.go
d29487c87997cd36edb63abd563163fd69fdd2668ab157ed4614407e2ba10cd3
```

The implementation assistant explicitly hand-authored the goals, metadata, relevance labels, and answer traces after reading production code. Labels were pinned before the first measured run and were not adjusted after seeing results. There are no real user records or independent human annotations. Reused topics and the paired interest scenarios are correlated; 64 candidate occurrences do not represent 64 independent users or tasks.

### Ranking Protocol

The harness constructs production `LearningNode` values, projects state with `LearningNodePublic`, then calls the actual private `rankRecommendations` in package `learning`. It scores the returned order, including page-kind diversity and the reserved final exploration slot.

After the first run, a product correction excluded mastered/not-due topics and placed due review ahead of page-kind diversity. Labels were retained and rerun. This is a development-set comparison, not an untouched heldout ranking evaluation. Original macro NDCG was 0.717003; the corrected value below is 0.813841.

Production weights are `0.45*review_need + 0.25*graph_frontier + 0.20*interest_match + 0.10*content_quality`. Both baselines receive the same candidates:

- `degree_only`: descending hand-assigned Wiki link degree, then ascending topic ID. Degree describes a hypothetical larger Wiki, not edges inferred from this small candidate subset.
- `cold_start`: descending source-metadata quality, then ascending topic ID. Quality is `(min(chunk_count,3) + 3*has_nonblank_summary)/6`. It ignores degree, personal state, interests, and relatedness. This is an explicitly defined evaluation baseline, not a claim about a separate deployed cold-start algorithm.

Labels express the stated goal: 3 immediate priority, 2 useful next practice, 1 acceptable exploration, 0 no immediate relevance. They are not computed from production scores. The interest-on/off pair has identical candidates and labels; only the supplied interest list changes.

`DCG@5 = sum((2^relevance_i - 1)/log2(i+1))`, with ranks starting at 1. `NDCG@5` divides by the ideal top-five DCG from all candidates. `Recall@5` is retrieved candidates with label > 0 divided by all candidates with label > 0, including those outside the returned five. No-positive cases are defined as zero for both metrics and checked arithmetically. Aggregate results are an unweighted mean over scenarios.

Measured values, with higher being better:

| Scenario | Production NDCG | Degree NDCG | Cold NDCG | Production Recall | Degree Recall | Cold Recall |
| --- | --- | --- | --- | --- | --- | --- |
| due_review_priority | 0.939490 | 0.484568 | 0.521337 | 0.833333 | 0.500000 | 0.666667 |
| related_frontier | 0.953888 | 0.281521 | 0.296158 | 1.000000 | 0.500000 | 0.500000 |
| optional_interest_on | 0.837277 | 0.047271 | 0.250158 | 0.750000 | 0.250000 | 0.500000 |
| optional_interest_off | 0.119219 | 0.047271 | 0.250158 | 0.500000 | 0.250000 | 0.500000 |
| exploration_slot | 0.949658 | 1.000000 | 1.000000 | 0.625000 | 0.625000 | 0.625000 |
| freshness_reset | 0.895830 | 0.698658 | 0.607774 | 0.800000 | 0.800000 | 0.600000 |
| cold_start | 0.815366 | 0.902402 | 0.949658 | 0.714286 | 0.571429 | 0.714286 |
| kind_diversity_tradeoff | 1.000000 | 0.973495 | 0.884972 | 1.000000 | 0.800000 | 0.600000 |
| Macro mean | 0.813841 | 0.554398 | 0.595027 | 0.777827 | 0.537054 | 0.588244 |

Production still loses NDCG to degree in 2/8 scenarios and to cold start in 3/8. Exploration and nonurgent diversity can lower immediate graded relevance. The correction prevents mastered pages from displacing due practice; it does not establish a general ranking advantage on real learners.

### Prediction Protocol

Each trace uses fixed prior mastery `p=0.20`, transition `0.15`, slip `0.10`, and guess `0.25`. Production `LearningAssess` applies only the strictly chronological training prefix, dated September 1-3. Holdout observations are dated September 4-6. Validation rejects overlapping/out-of-order timestamps, repeated question IDs within a trace, missing labels, future observations, and empty holdouts.

The model is frozen at the split. All three heldout answers receive the training-only response probability:

```text
q = P(correct response) = 0.90*p + 0.25*(1-p)
```

Heldout outcomes never update mastery, select parameters, or enter the predictor. This frozen-tail protocol does not measure online adaptation during the holdout. The baselines always predict `q=0.25` for four-option chance and `q=0.38` for the fixed mastery prior. Scoring latent `p=0.20` directly as a correct-response probability would be incorrect.

`Brier = mean((q-y)^2)`. `LogLoss = mean(-y*ln(q) - (1-y)*ln(1-q))`, using natural logs. Both are micro-averaged over the 24 heldout observations; lower is better. No parameters are fitted and no outcomes are sampled from BKT to manufacture agreement.

| Predictor | Brier | Log Loss |
| --- | --- | --- |
| BKT response | 0.277601 | 0.780492 |
| Fixed chance, q=0.25 | 0.312500 | 0.836988 |
| Fixed prior response, q=0.38 | 0.264400 | 0.722810 |

The steady-success trace gives BKT Brier `0.016127`; success followed by all wrong heldout answers gives `0.762145`. The reversal is retained. Aggregate BKT loss is worse than fixed prior, so this fixture provides no evidence of a general predictive advantage over that baseline.

### Arithmetic And State

Golden constants were independently calculated using rational hidden-state masses and checked with separate fraction arithmetic, not obtained from the production functions. For example, a first correct answer changes the known posterior to `9/19`, transitioned mastery to `21/38`, and next response chance to `463/760`. Twelve fixed prefixes cover both correct and incorrect transitions, including every training pattern in the dataset.

The metric golden retrieves grades `[3,0,1,0,2]` against ideal `[3,2,2,1,0]`: NDCG is `0.8001649902816075` and Recall is `3/4`. Two predictions, `(q=0.25,y=1)` and `(q=0.75,y=0)`, give Brier `9/16` and log loss `ln(4)`. Separate checks cover weighted recommendation arithmetic, reason codes, baseline ordering/ties, and reversed-input determinism.

State checks exercise production projection, optional-memory familiarity, assessment reset, inclusive mastery/review thresholds, and review intervals. Reading and familiar-document signals never add mastery credit. Changed/missing stamps hide old credit; assessment on a new source starts from the prior and preserves familiarity. These are in-memory state checks; persistence, answer idempotency, admission, and source-fetch correctness remain the responsibility of the separate backend tests.

### Evidence Limits

This is a small, assistant-authored proxy/regression benchmark whose author knew the algorithm. It is neither blinded nor independent, and its scenario mix can strongly change aggregate results. It cannot establish population calibration, statistical significance, learner retention, or human learning improvement. No participant count, human-label agreement, or human effect size is claimed.

Candidate retrieval, full-graph recall, real memory switches, UUID admission, live source validation, and quiz generation are outside this harness. Short topic IDs, chunk references, degree values, and source stamps are synthetic metadata. In particular, the missing-evidence scenario values revisiting a topic; it does not assert that a quiz can be served from missing evidence. Related-topic links are not prerequisite evidence.

Successful source validation checks quotation and freshness consistency; it is not semantic proof that a question or explanation is correct. A blinded pass using the same underlying model can share the generator's errors. Same-model agreement is not an independent human check and is not measured here.

For claims beyond these fixtures, collect consented, independently reviewed cases and temporally heldout real answers with a stated sampling policy. Human learning-gain claims additionally require an appropriate participant study. Preserve this fixture's labels when changing algorithms; changes to scenarios, labels, or metric definitions require a new dataset version, hash, and measured report.
