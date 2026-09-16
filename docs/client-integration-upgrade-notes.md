# Client and integration upgrade notes

## Upgrade requirements

- **DingTalk:** HTTP callbacks are disabled. PostgreSQL migration 000096 and
  SQLite migration 000017 convert DingTalk `webhook` channels to `websocket`.
  Enable **Stream** in the DingTalk developer console using the existing
  application credentials. Other platforms and channel settings are preserved.
  The down migrations retain Stream mode.
- **Signing keys:** configure a unique `SYSTEM_SIGNING_KEY` (for example,
  `openssl rand -hex 32`), or retain a valid private `SYSTEM_AES_KEY` as the
  signing fallback. Pass the same key to all replicas. Known example keys cannot
  sign links or embed sessions. The native desktop creates a persistent,
  owner-only `signing.key` under its existing user configuration directory.
  Do not overwrite an existing AES encryption key: that would make encrypted
  credentials unreadable. Signing-key changes invalidate outstanding signed
  links. All previous embed session handles are invalidated by the new signing
  algorithm; visitors need a new session.
- **Lite:** anonymous HTTP auto-setup is no longer available. Native desktop
  auto-login uses a random per-process capability obtained through the Wails
  bridge. Browser clients use normal registration/login.
- **Organization invitations:** the candidate picker resolves an exact workspace
  ID. Global workspace-name search is removed. Invitation links remain supported.
- **MCP uploads:** stdio now defaults to the working-directory upload boundary,
  matching HTTP/SSE. Set `MCP_ALLOWED_UPLOAD_DIRS` to explicit trusted directories
  if uploads outside that directory are required; symbolic links escaping those
  directories are rejected.
- **MCP OAuth:** frontend redirects must be application-relative or use the
  exact origin in `APP_EXTERNAL_URL`. Configure canonical MCP/OAuth endpoint URLs;
  cross-origin redirects from their HTTP clients are refused.
- **IM webhooks:** configure the platform verification secret for each supported
  HTTP channel. Empty secrets are rejected. Gateway/Stream channels cannot be
  reached through the HTTP callback endpoint. Slack downloads require
  `https://files.slack.com` or legacy `https://slack.com/files-pri/` URLs;
  DingTalk session replies require its exact platform
  endpoint.
