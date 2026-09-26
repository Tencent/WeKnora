# Jira example plugin

Syncs the issues of selected Jira Cloud projects, with their comments, into a
knowledge base. It also ships a `jira-triage` skill that looks for duplicates,
related work and owners among the synced issues.

It is the fullest example plugin. It shows:

- a connector with incremental sync, checkpoints and deletions;
- agent tools the plugin serves itself (`search_issues`, `get_issue`), with
  result views: a table of issues, and the issue as Markdown;
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
3. Workspace admins switch the plugin on under **Settings → Plugins**.
   - To sync, add a **Jira** data source to a knowledge base.
   - For the agent tools, fill in the plugin's configuration there (an
     Atlassian account or an API token). The tools then appear as the
     **Jira** MCP service, ready to add to agents.

## What is synced

Each issue becomes one Markdown document:

- **Title:** `<KEY> <summary>`.
- **Facts:** type, status, priority, resolution, assignee, reporter, labels,
  dates, parent, and a link.
- **Description**, converted from Atlassian Document Format.
- **Comments**, when enabled.

Attachments are not synced.

**Incremental syncs** fetch the issues updated since the newest one seen, in
the user's time zone, as JQL requires. When Jira does not reveal the time
zone (a profile can hide it), they look back 12 hours more, enough for any
zone.

**Full syncs** also remove the documents of issues that were deleted, moved
out of the selected projects, or no longer match the filters, and of the
projects no longer selected. A full sync checkpoints each page: when it
fails part way (a timeout, an expired OAuth token), the retry goes on from
there.

**Live updates.** The plugin's `issues` webhook (its URL is in Settings →
Plugins → Jira) takes Jira's issue events. Register it in Jira (Settings →
System → WebHooks) for issue created, updated and deleted.
- Each event syncs the data sources following the issue's project right
  away, through the Host API's `datasources` scope.
- An incremental sync only sees updated issues, so a delete event also
  leaves a note in the plugin's key-value store (scope `kv`) for a week.
  The sync removes a noted issue once Jira confirms it is gone, so a
  forged event deletes nothing.
- If the Jira webhook has a secret, enter it in the plugin's configuration;
  calls without a matching `X-Hub-Signature` are refused.

## Test

```bash
go test ./examples/plugins/jira/
```

The tests run the plugin against a fake Jira Cloud site, both directly with
an API token and through the OAuth gateway.
