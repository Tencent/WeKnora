# Jira example plugin

Syncs the issues of selected Jira Cloud projects, with their comments, into a
knowledge base. It also ships a `jira-triage` skill that looks for duplicates,
related work and owners among the synced issues.

It is the fullest example plugin. It shows:

- a connector with incremental sync, checkpoints and deletions;
- an OAuth field (`x-oauth`): WeKnora runs Atlassian's consent flow and hands
  the plugin a fresh access token at every call;
- dynamic choices (`x-options`): the sites the account can reach and the
  site's issue types;
- form messages in the user's language (`call.Locale`);
- a skill in the package.

## Install

1. Build the package with `./package.sh` and install it under **System
   administration → Plugin management**.
2. To let workspaces sign in with their Atlassian accounts, register an app
   first. Without it, workspaces can still use an API token.
   1. In the [Atlassian developer console](https://developer.atlassian.com/console/myapps/),
      create an OAuth 2.0 (3LO) app.
   2. Under **Permissions → Jira API**, add `read:jira-work` and
      `read:jira-user`.
   3. Under **Authorization**, set the callback URL to
      `<WeKnora address>/api/v1/plugin-oauth/callback`.
   4. In the plugin's details in WeKnora, enter the app's client ID and
      secret.
3. Workspace admins switch the plugin on under **Settings → Plugins**, then
   add a **Jira** data source to a knowledge base.

## What is synced

Each issue becomes one Markdown document:

- **Title:** `<KEY> <summary>`.
- **Facts:** type, status, priority, resolution, assignee, reporter, labels,
  dates, parent, and a link.
- **Description**, converted from Atlassian Document Format.
- **Comments**, when enabled.

Attachments are not synced.

**Incremental syncs** fetch the issues updated since the newest one seen, in
the user's time zone, as JQL requires.

**Full syncs** also remove the documents of issues that were deleted, moved
out of the selected projects, or no longer match the filters.

## Test

```bash
go test ./examples/plugins/jira/
```

The tests run the plugin against a fake Jira Cloud site, both directly with
an API token and through the OAuth gateway.
