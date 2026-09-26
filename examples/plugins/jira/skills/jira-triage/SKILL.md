---
name: jira-triage
description: Triage a Jira issue against the issues synced into the knowledge base - find duplicates, related work, likely owners and a suggested priority. Use when the user pastes a Jira key or a bug report and asks whether it is known, who should take it, or how urgent it is.
---
# Jira triage

The Jira data source stores each issue as one Markdown document titled
`<KEY> <summary>`. It starts with a fact list (Type, Status, Priority,
Resolution, Assignee, Reporter, Labels, Created, Updated, Parent, Link), then
`## Description` and `## Comments`.

## Steps

1. **Understand the report.** If the user gave a key (`ENG-123`), find that
   issue first with `search_knowledge` on the key and read it with
   `read_document`. Otherwise work from the text they pasted. Note the
   component, the symptom, error messages and the version or environment.
2. **Look for duplicates.** Search with the symptom in a few phrasings and
   with any exact error message. Read the closest matches. Treat as a
   duplicate only when the symptom and the conditions both match, not only
   the area.
3. **Find related work.** Look for open issues in the same area, recent
   fixes (Resolution set, Updated recently) and parent epics.
4. **Suggest an owner.** Prefer the assignee of the duplicates or of recent
   fixes in the same area. Say how many issues support the suggestion.
5. **Suggest a priority.** Compare with the priorities of similar issues and
   weigh impact (data loss, security, many users) over annoyance.

## Answer

Reply in the user's language with:

- **Verdict:** duplicate of `<KEY>`, related to `<KEYS>`, or new.
- **Evidence:** one line per issue: key, summary, status, and why it
  matches. Link each with the Link from its document.
- **Owner** and **priority** suggestions, with the reason.
- **Missing information** the reporter should add, if any.

Only cite issues you read. The knowledge base may lag Jira by one sync; say so
when the newest issue you found is older than a day.
