# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

**Every agent run starts with an empty context window.** Nothing you learned in a previous run comes back on its own, so the read below is not a warm-up: it is how vocabulary and settled decisions reach you at all. Do it before the first file you open, every run.

- **`GLOSSARY.md`** at the repo root, or
- **`GLOSSARY-MAP.md`** at the repo root if it exists: it points at one `GLOSSARY.md` per context. Read each one relevant to the topic.
- **`docs/adr/`**: read ADRs that touch the area you're about to work in. In multi-context repos, also check `src/<context>/docs/adr/` for context-scoped decisions.

If any of these files don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The `domain-modeling` skill (reached via `grill-with-docs` and `improve-codebase-architecture`) creates them lazily when terms or decisions actually get resolved.

## File structure

Single-context repo (most repos):

```
/
├── GLOSSARY.md
├── docs/adr/
│   ├── 0001-event-sourced-orders.md
│   └── 0002-postgres-for-write-model.md
└── src/
```

Multi-context repo (presence of `GLOSSARY-MAP.md` at the root):

```
/
├── GLOSSARY-MAP.md
├── docs/adr/                          ← system-wide decisions
└── src/
    ├── ordering/
    │   ├── GLOSSARY.md
    │   └── docs/adr/                  ← context-specific decisions
    └── billing/
        ├── GLOSSARY.md
        └── docs/adr/
```

Default to single-context. Reach for `GLOSSARY-MAP.md` only in a genuinely large multi-package repo, detected by a `pnpm-workspace.yaml`, a `workspaces` field in `package.json`, or populated `packages/*` directories that each carry their own `src/`. Its absence means single-context; that is almost every repo.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `GLOSSARY.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for the `domain-modeling` skill).

**Write the term down the moment it is resolved.** In this platform, a run that ends holds nothing: a term agreed in conversation but absent from `GLOSSARY.md` is gone by the next run. Resolved terminology and accepted decisions belong in the repo, not in the transcript.

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0007 (event-sourced orders), but worth reopening because…_

## Concurrent runs and ADR numbering

**Runs do not share state, and they can overlap.** Two agents deciding different things in the same area will both read the highest ADR number, both claim the next one, and one will overwrite or duplicate the other. ADR numbering is the one part of the domain docs that cannot be fixed by re-reading.

Rules:

- **Claim the number by writing the file first**, with the next free number and a stub decision, before you finish the argument that fills it in. A file on disk is visible to another run; an intent in your head is not.
- **Re-read `docs/adr/` immediately before writing**, not at the start of the run. The gap between exploration and writing is exactly when a competing run lands.
- **If the number you wanted is taken, take the next free one and leave the reference intact.** Never renumber an existing ADR; the number is its identity and other documents cite it.
- **Prefer updating an existing ADR over adding a near-duplicate.** Two ADRs answering one question is worse than one that was edited.
- **Surface the collision instead of resolving it silently.** If you find two ADRs claiming one number, do not pick a winner: say so in the issue comment and let a human rule.
