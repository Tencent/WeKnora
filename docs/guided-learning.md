## Guided Learning

Guided learning connects Wiki pages to a personal practice history. A learner reads a source-backed topic, answers a multiple-choice question, and receives a reproducible mastery estimate and a next-topic recommendation. The feature adds personal learning tables, a Wiki learning panel and three Agent tools; existing memory and retrieval behavior is unchanged.

Baseline: `3e6010e7`, branch `feat/guided-learning`. This design supersedes the old local `docs/dev/guided-learning-design.md` draft.

### Scope

- Explicit per-user opt-in. Learning can run without long-term memory; optional interest and document-affinity signals obey existing memory switches.
- V1 admits authenticated Web users and Wiki content owned by their active workspace. Shared knowledge bases, shared Agents, API keys, IM and Embed identities are excluded.
- A published entity/concept/synthesis/comparison Wiki page is a topic. Its UUID is the identity. Rename preserves UUID and history; ordinary graph links express related topics, never prerequisites.
- Three source-backed single-choice questions per quiz. Answers are submitted by the human through HTTP, never through an Agent tool.
- Personal overview, deterministic recommendations, Wiki graph overlay, explanations with source quotes, export and physical deletion are included.
- Long course plans, inferred prerequisite graphs, subjective grading, learning-state merging between unrelated pages and semantic recommendation embeddings are deferred.

### Evidence And Mastery

Reading and historical document citations affect familiarity only. BKT updates use server-graded first answers to distinct questions. Every answer records the question fingerprint, current source stamp, before/after probability and algorithm version. Replaying an attempt or guessing the same question again never increases mastery.

The default BKT model uses initial mastery 0.20, learning transition 0.15, guess 0.25 (four options), and slip 0.10. A topic is marked mastered only with at least three distinct answers and probability >= 0.85. Review intervals are 1, 3, 7, 14 and 30 days. The probability is an uncalibrated model estimate, not a measured human proficiency score.

Quiz generation uses current enabled source chunks in the same live knowledge base. Question schemas, four distinct options, answer index and exact normalized evidence quotations are checked deterministically. A separate blinded model pass answers each proposed question from its source evidence and rejects disagreements or ambiguity. These checks reduce generation errors; they do not prove pedagogical quality or replace human review.

Source stamps include the actual page input, page version, source membership, chunk IDs, revisions and content hashes. Freshness is checked before generation, publication, serving a quiz and accepting an answer. Missing, disabled, moved or changed evidence makes the quiz stale. No old answer updates current mastery.

### Execution And Isolation

PostgreSQL is authoritative for profile, quiz, questions, attempts and mastery. SQLite has equivalent migrations for Lite. Asynq carries wake-up messages only. Quiz claims use expiring leases with fencing tokens; a bounded periodic recovery scan recreates missing triggers and reclaims expired claims.

Opt-out and deletion serialize with quiz publication and answer submission through the profile row. A monotonic epoch invalidates old work, including a delete/re-enable race. Transactions recheck live KB/page/source bindings; no transaction is held across a model call. Recovery also removes data for deleted content. HTTP, tools and worker publication each enforce their own admission checks.

Each quiz snapshots its complete source-document ID set. Export and recovery physically remove quizzes, answers and matching-source mastery when a document is deleted or moved out of scope, even if Wiki cleanup preserves the page and removes its old references. A newer assessment from surviving sources remains intact. Ordinary content edits stale a quiz without deleting historical answers.

The immutable authenticated Caller supplies the personal tenant, paired with the Web principal's StorageID. An execution-tenant mismatch is rejected. Agent tools accept only complete KB scopes in v1; narrowed document/tag scopes cannot widen into a whole learning graph.

Model input contains source material and no personal profile or prior answers. Generation and validation calls use content-redacted diagnostics. API DTOs omit answer keys and explanations until submission. Agent transcripts contain quiz IDs and public state only, never answer keys. Privacy controls remain available after opt-out.

### Recommendation

Bounded candidates come from due practice, related topics, title/alias interest matches and cold-start topics. Ranking uses `0.45 * review_need + 0.25 * graph_frontier + 0.20 * interest_match + 0.10 * content_quality`, deterministic tie-breaking, diversity selection and a reserved exploration position. Scores and reason codes travel with each recommendation; the UI and Agent must not invent reasons.

### Validation

- Unit tests: BKT golden sequences, independent prediction checks, quote/schema validation, recommendation scoring and idempotency.
- Database tests: PostgreSQL and SQLite migrations, concurrent submissions, epoch/lease races, deletion and stale-source fences.
- Integration: generated quizzes with a real configured model; adversarial/malformed responses use fakes.
- Browser: opt-in, recommendation, source reading, quiz submission, graph state, refresh, export and deletion across desktop/mobile.
- Effectiveness: a checked-in synthetic offline benchmark compares recommendations and predictions with named baselines. Report dataset hash, sample count, model/prompt version and measured outcomes. Human learning gains and human-label agreement remain unmeasured unless real participants provide evidence.

Implementation contracts and progress: [development tasks](guided-learning-contract.md). Runtime configuration and credentials live in ignored `.runtime/`, outside versioned artifacts.
