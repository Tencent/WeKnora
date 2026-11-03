# Nextcloud data source

The `nextcloud` connector imports selected folders and files from a Nextcloud account over WebDAV. It supports scheduled, incremental and resumable synchronization, including deletion reconciliation. See [Data source synchronization](10-datasource.md) for common scheduling and conflict settings.

<Screenshot src="/screenshots/datasource-nextcloud.png" caption="Nextcloud connection settings" hint="The real WeKnora component with example credentials and a stubbed API; the app password is masked." />

## Account and app password

1. Sign in to Nextcloud with an account that can read the intended folders.
2. Open **Personal settings → Security → Devices & sessions**, name a new app password (for example, `WeKnora`), and create it. Copy the generated username and app password; the password is displayed only once.
3. Use those credentials in WeKnora. Use an app password rather than the account's login password, including when two-factor authentication or an external login provider is enabled. Revoke or rotate it from the same Nextcloud settings page.

The connector uses HTTP Basic authentication for both PROPFIND and GET requests. Use HTTPS for connections outside a trusted network. Credentials use the existing encrypted data-source credential JSON; API responses expose only whether credentials are configured. Replacing credentials requires entering the whole credential set again.

Official references: [Nextcloud app passwords](https://docs.nextcloud.com/server/stable/user_manual/en/session_management.html) and [WebDAV file operations](https://docs.nextcloud.com/server/stable/developer_manual/client_apis/WebDAV/basic.html).

## Configure in WeKnora

1. Open **Knowledge base settings → Data sources → Add**, then choose **Nextcloud**.
2. Enter the installation's base URL, username and app password. Use `https://nextcloud.example.com`, or `https://nextcloud.example.com/cloud` for an installation with a path prefix. Do **not** append `/remote.php/dav`, `/files/alice`, or a folder path. The connector constructs `/remote.php/dav/files/{username}/` itself.
3. Test the connection. This checks account access with a Depth-0 PROPFIND; it does not enumerate the account or download files.
4. Optionally enter comma-separated include/exclude extensions, such as `.pdf, .md`. Matching is case-insensitive; a leading dot is optional. An empty include list allows all supported import formats, and exclusions take priority. These values live in `config.settings.include_extensions` and `config.settings.exclude_extensions`; the API also accepts arrays of strings.
5. Expand the resource tree and select at least one folder or file. Select the top-level **Nextcloud** folder to sync the whole account. A selected folder includes future descendants; overlapping selections are collapsed. Resource IDs are absolute, decoded paths relative to the account root, such as `/docs`, not WebDAV URLs or numeric file IDs.
6. Choose the sync schedule, conflict policy and **Sync deletions** setting. Save and manually sync a small folder first.

<Screenshot src="/screenshots/datasource-nextcloud-resources.png" caption="Selecting a folder in the lazy resource tree" hint="Example folder data from a stubbed API; this screenshot does not demonstrate a live Nextcloud connection." />

The account must have permission to list every directory in the selected scope. Public-share links, end-to-end encrypted content, activity feeds and two-way synchronization are outside this connector's scope.

## Network and SSRF protection

The base URL passes the upstream `utils.ValidateURLForSSRF` policy through `datasource.ValidateConnectorBaseURL`. Every request uses the shared SSRF-aware transport, dial checks and redirect policy. There is no connector-specific allowlist or bypass.

For a private/self-hosted deployment, an administrator must add the Nextcloud host to the existing **`SSRF_WHITELIST`** configuration and restart the backend. For example:

```dotenv
SSRF_WHITELIST=nextcloud.example.com
```

Preserve any other hosts already required by your deployment. The shared policy also supports `SSRF_WHITELIST_EXTRA` for deployment-managed additions. Both PROPFIND and downloads stay on the configured origin. Cross-origin redirects, including changes of scheme or port, are rejected even if the target is whitelisted; configure the canonical HTTPS installation URL directly. The connector never follows response hrefs as download URLs: it validates them and builds requests under the account's WebDAV root.

## Incremental synchronization and deletion

Each path has a cursor fingerprint containing the ETag, last-modified time, size and optional Nextcloud file ID. At least an ETag or last-modified time is required for files. Unchanged fingerprints skip downloads; each run still enumerates the complete selected scope because directory timestamps alone cannot establish which files were deleted.

Downloads use `If-Match` for strong ETags, or `If-Unmodified-Since` for the last-modified fallback. Returned validators and byte counts must match the listing. Weak ETags or missing response validators require a second Depth-0 metadata check before the cursor acknowledges the file.

| Source change | Result |
| --- | --- |
| Added or modified file | Download and emit a new/updated item |
| Unchanged file | Skip download |
| Deleted file | Emit `IsDeleted` after a complete successful scan; the service applies **Sync deletions** |
| Renamed, moved, or moved outside the selection | Import the new path if selected; reconcile the old path as a deletion |
| Selected root removed or renamed | Stop the run without deletions; reselect the intended scope |
| Selection or extension filters changed | Reconcile previously tracked paths outside the new scope as deletions |
| Server or account changed | Reject the existing cursor; create a new data source |
| App password rotated | Keep the cursor and source identities |

Knowledge files retain the account-relative hierarchy under `Nextcloud/`. Names are sanitized segment by segment while preserving supported extensions and non-ASCII names. Source identity includes the deployment/account scope and the original path, so identical content at two paths remains independent.

The streaming interface holds one file body at a time. Checkpoints contain complete cursor snapshots, saved every 50 cursor mutations, before deletions and at completion. Interrupted runs resume from acknowledged items. Forced full sync re-downloads all files and preserves the prior deletion baseline across checkpoints and retries.

Directory failures, malformed or incomplete multistatus responses, and traversal-limit failures stop the run without deletion reconciliation. Per-file permission, missing-file, download, size and version errors produce localized error items; other files continue, and failed paths are retried next run. HTTP 401 stops the run as invalid credentials. Requests are bounded by a 120-second client timeout and the sync context; failed requests are retried on the next sync rather than in an unbounded request loop.

As with other data sources, deletions observed while **Sync deletions** is disabled are removed from the cursor and are not retrospectively deleted when it is enabled again. Removing a data source does not clean up already imported knowledge. A successful connector download is distinct from downstream document parsing/indexing success.

## Formats and limits

- Supported extensions follow `internal/utils/import_filetypes.go`, the same list used for knowledge uploads. The picker always shows supported formats, so filters can be edited without saving them before browsing; include/exclude filters apply at sync time. Image/audio ingestion still requires the corresponding knowledge-base model configuration.
- Files use `MAX_FILE_SIZE_MB` (default **50 MiB**). Oversized listed files are rejected before GET. Downloads read at most the limit plus one byte; an oversized or partial body is never ingested. Align the document parser's `DOCREADER_GRPC_MAX_FILE_SIZE_MB` when increasing this limit.
- PROPFIND uses only **Depth 0 or 1**. WebDAV provides no standard PROPFIND pagination, so the connector accepts only complete responses: at most **32 MiB** and **100,000 entries** per listing, **1,000,000 listed entries** per run, and a traversed directory depth of **256**. Exceeding a limit fails the scan rather than silently truncating it. Select smaller folders if necessary.
- Responses must use DAV namespaces and valid properties. Encoded paths are decoded exactly once; out-of-scope hrefs, duplicates, dot segments and incomplete responses are rejected. Reverse proxies must preserve PROPFIND, Depth, authentication and conditional request headers.

Sync error codes are `nextcloud_permission_denied`, `nextcloud_not_found`, `nextcloud_file_too_large`, `nextcloud_empty_file`, `nextcloud_source_changed`, `nextcloud_invalid_response`, `nextcloud_ssrf_blocked`, and `nextcloud_fetch_failed`. Authentication errors and invalid configuration are reported at the data-source level. Server response bodies, request URLs and app passwords are never included in connector errors.

## Verification

Unit tests use an in-process WebDAV server and need no Nextcloud instance:

```bash
go test ./internal/datasource/connector/nextcloud -count=1
```

The optional read-only live test is skipped unless **all four** variables are set: `NEXTCLOUD_LIVE_URL`, `NEXTCLOUD_LIVE_USER`, `NEXTCLOUD_LIVE_APP_PASSWORD`, and `NEXTCLOUD_LIVE_ROOT`. Configure them locally; do not commit credentials. The root must be a small test folder containing a supported, non-empty file. Private deployments still need the normal SSRF whitelist.

```bash
go test ./internal/datasource/connector/nextcloud -run '^TestLive$' -count=1 -v
```

It checks account access, lists the root, downloads one supported file and verifies that a second incremental run skips it. It never writes or deletes source content and does not print live credentials or filenames. Keep that file unchanged during the check.
