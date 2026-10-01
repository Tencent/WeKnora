# Issue tracker: Multica

Issues and specs for this repo live as Multica issues in this workspace. Use the `multica` CLI for all operations. There is no `gh`/`glab` step and no PR surface: Multica has no pull request object.

## Conventions

- **Create an issue**: `multica issue create --title "<t>" --description-file <f> --project <id> --output json`. Body comes from a file (write it first), never an inline multi-line string. Add `--parent <UUID>` to nest it under a map or spec, `--stage <n>` to place it in an ordered barrier layer, `--assignee <agent|member|squad>` to hand it to someone, `--priority <p>` if priority matters. The JSON result carries `identifier` (human handle, e.g. `STUD-11`) and `id` (the UUID every other command wants). Record both in the issue body when other work must reference this one.
- **Labels are a second step**. `issue create` has **no** `--label` flag. Create the issue, then add labels by UUID: `multica issue label add <issue-id> <label-id>`. Resolve names to UUIDs with `multica label list --output json` first; `label add`/`label remove` reject names.
- **Read an issue**: `multica issue get <id> --output json`, then read its discussion: `multica issue comment list <id> --roots-only --summary --compact --output json`, expanding a thread with `multica issue comment list <id> --thread <comment-id> --tail 30`.
- **List issues**: `multica issue list --status <key> --fields id,identifier,title,status,labels,stage,parent_issue_id,assignee_type,assignee_id --output json`. `--status` takes a status key; an unknown key returns an **empty page without an error**, so a zero result is never proof that nothing exists. Drop `--status` to see everything.
- **Search**: `multica issue search "<q>"` for full-text lookup when you have a phrase, not an id.
- **Comment on an issue**: `multica issue comment add <id> --content-file <f>`. **A comment-triggered run must reply inside the thread that triggered it**: `multica issue comment add <id> --content-file <f> --parent <triggering-comment-id>`. A top-level comment in that situation is rejected by the platform.
- **Apply / remove labels**: `multica issue label add <issue-id> <label-id>` / `multica issue label remove <issue-id> <label-id>`. One status label plus one category label; remove the old one in the same pass as you add the new one.
- **Status**: `multica issue status <id> <status-key>`. Keys: `backlog`, `todo`, `in_progress`, `in_review`, `done`, `blocked`, `cancelled`. An unknown value here **does** error. Closing an issue means `multica issue status <id> done` (a status change, not a separate close verb).
- **Assignment**: `multica issue assign <id> --to "<name>"` or `--to-id <uuid>`; `--unassign` releases it. `assign`, `status`, and `update` **start a run for the new assignee by default**; add `--no-start` when the write is bookkeeping only (recording who drives a map, re-framing a label) and nobody should wake up.

## Pull requests as a triage surface

**PRs as a request surface: no.** _(This flag exists for parity with the GitHub and GitLab templates. Multica exposes no pull request object, so there is nothing for `triage` to read here; leave it at `no`.)_

## When a skill says "publish to the issue tracker"

Create a Multica issue: write the body to a file, then

```bash
multica issue create --title "<title>" --description-file <body.md> --project <project-id> --output json
```

Capture `identifier` and `id` from the JSON. Labels are a follow-up step (`multica issue label add <issue-id> <label-id>`), never a create flag. When the ticket belongs under a map or spec, pass `--parent <parent-UUID>` at create time; a ticket published first and parented later loses its place in `issue children` grouping unless you re-read.

## When a skill says "fetch the relevant ticket"

Run

```bash
multica issue get <id> --output json
multica issue comment list <id> --roots-only --summary --compact --output json
```

and expand any thread that looks decision-bearing with `--thread <id> --tail 30`. Read the body before the comments: the body is the brief, the comments are the history.

## Wayfinding operations

Used by `wayfinder`. The **map** is a single issue with **child** issues as tickets. Before any of this, read the map: `multica issue get <map-UUID> --output json`.

- **Map**: a single issue labelled `wayfinder:map`, holding the Destination / Notes / Decisions-so-far / Not yet specified / Out of scope body. Create it with `multica issue create --title "<map name>" --description-file <map.md> --output json`, then label it via `multica issue label add <map-UUID> <wayfinder:map-label-UUID>`. The label is a note to humans, not something the platform enforces: nothing stops a stray issue from carrying it, so trust the Destination section, not the label alone.
- **Child ticket**: `multica issue create ... --parent <map-UUID> --output json`. The hierarchy is the link; there are no GitHub-style sub-issue APIs to call. Carry `wayfinder:<type>` (`research`/`prototype`/`grilling`/`task`) as a label added after creation, and put the ticket's question in `## Question`. Once claimed, the ticket is assigned to the dev driving the map. Refer to tickets by title in everything a human reads, never by bare UUID.
- **Blocking (degraded)**: Multica has **no native dependency edge**. Express blocking as an ordered barrier plus text:
  1. Create the blockers first, and give each layer a higher `--stage <n>` than every layer it waits on. All tickets in `--stage <n>` must complete before the parent issue's assignee is woken, so a stage is a **whole-layer barrier**.
  2. Put `## Blocked by` at the top of the child body listing each blocker by title and identifier (e.g. `- <blocker title> (STUD-11)`), or `None (can start immediately)`.
  3. Wire the text in a **second pass**, after the blockers exist and have identifiers; an issue cannot reference an identifier it does not have yet.

  The cost, stated plainly: a stage barrier cannot express cross-dependencies. A DAG that is not a clean layering (A waits on B, C waits on B, D waits on C while E waits on D and C) has to be flattened into layers, and tickets that are genuinely independent end up sharing a barrier because something else in their layer is slow. A ticket is **unblocked** when every issue in every lower stage is `done` **and** every ticket named in its `## Blocked by` is `done`. The platform enforces neither half; the reader does.
- **Frontier query**: get the map's children as the platform groups them: `multica issue children <map-UUID> --output json`, which returns `stages[]` (grouped, one entry per `--stage`) and `unstaged[]`. Walk `stages[]` in ascending stage order; within the first stage that still has open children, take the open children. Then apply the human half of the rule by hand: drop any child still `blocked`, any child with an assignee (an assignee **is** the claim), and any child whose `## Blocked by` names an issue that is not `done`; resolve each named blocker with `multica issue get <blocker-UUID>`. First in map order wins.
- **Claim**: `multica issue assign <issue-UUID> --to "<driver name>"`, the session's first write, before any work. Add `--no-start` only when you are recording a claim without intending to wake a run; a normal claim wants the run.
- **Resolve**: post the answer as a resolution comment inside the deciding thread (`multica issue comment add <issue-UUID> --content-file <answer.md>`, plus `--parent <thread-id>` when the resolution belongs in an existing discussion), then `multica issue status <issue-UUID> done`, then append a one-line context pointer to the map's Decisions so far. Update the map body with `multica issue update <map-UUID> --description-file <map.md>` and pass `--no-start` so the map edit does not wake the map's assignee.
- **Out of scope**: `multica issue status <issue-UUID> cancelled`, then leave one line in the map's Out of scope section naming the ticket, the gist, and why it sits past the destination. A cancelled ticket is unambiguously off the frontier; it never graduates.
- **Waking a human on an asynchronous turn**: when the answer has to come from a specific person, register the wakeup rather than hoping the comment is seen:

  ```bash
  multica issue wakeup create <issue-UUID> --event comment.created \
    --filter-actor-type member --filter-actor-id <user-uuid> \
    --instruction "<what to do when that comment lands>"
  ```

  Then end the run with a comment that states the one question, the options, and your recommendation.

### The discipline the degradation forces

Because the platform validates neither the barrier nor the text:

1. **Create then wire.** Never write `## Blocked by` in the same pass that creates the blockers: the identifiers do not exist yet.
2. **Re-read the layers after every batch.** `multica issue children <map-UUID> --output json` is the cheapest check that the stage numbers actually match the dependency story you told in prose.
3. **Never let prose and stage drift.** A ticket whose `## Blocked by` says one thing and whose stage says another is worse than either alone: the barrier wakes work early, the prose holds it back, and the next agent picks one at random.
4. **Text is the only cross-link.** An identifier mentioned in body text is not a relationship; it is a string. Any tool that wants the graph has to build it by reading bodies.
