# Tenant RBAC Guide

How WeKnora enforces who-can-do-what inside a tenant, how to roll the
feature out without breaking existing deployments, and how to audit
the system once it is on.

> Status: shipped behind a feature flag (`tenant.enable_rbac`,
> default `false`). Schema and `tenant_members` rows are populated
> on every install; enforcement is opt-in.

## Why this exists

Before #1303 every authenticated user with `X-API-Key` or a JWT was
effectively an Admin in their tenant. That's fine for a single-user
self-host, but as soon as a tenant has more than one human (e.g. a
small team sharing a knowledge base) you need to draw lines around:

- who may delete a knowledge base or revoke an API key (Admins);
- who may upload documents and edit their own KB (Contributors);
- who may only read and ask questions (Viewers).

RBAC layers a **per-tenant role matrix** on top of the existing JWT /
API-key auth so all three states are first-class.

## The role matrix

Every tenant member has exactly one role, stored in `tenant_members.role`:

| Role          | Typical use                                  | Notable powers |
|---------------|----------------------------------------------|----------------|
| `viewer`      | Read-only consumer (chat, search, browse).   | None — reads only. |
| `contributor` | Upload + edit **own** KBs / agents.          | Can mutate resources whose `creator_id` matches their user ID. Cannot touch other contributors' resources. |
| `admin`       | Tenant-wide operator.                        | Can mutate any resource in the tenant; manages members; configures shared infrastructure (Ollama, parser, storage, etc.). |
| `owner`       | Tenant founder.                              | Same as Admin, plus may delete the tenant and cannot be demoted by another Admin. Exactly one Owner per tenant after backfill. |

Higher roles inherit lower roles' permissions. The hierarchy is
`viewer < contributor < admin < owner`.

### Special cases the auth layer still handles

- **Cross-tenant superusers** (`User.CanAccessAllTenants` + the
  `enable_cross_tenant_access` flag + `X-Tenant-ID` header) get an
  Admin-level pass into the target tenant without needing a row in
  `tenant_members`. Used by org-level operators who administer many
  tenants.
- **API-key callers** (`X-API-Key` synthetic users) are pinned to
  Admin in the tenant the key belongs to. This preserves every
  scripted-integration use case except tenant deletion (which still
  requires Owner).
- **Orphan tenants** (zero `tenant_members` rows — typically
  API-key-only tenants) auto-promote the first authenticating human
  to Owner. Prevents lock-out after a fresh install.

## How enforcement is gated

Two routes a request can take:

```text
                           ┌─────────────────────┐
JWT / API-key ──► auth ──► │  tenant_members     │
                           │  lookup → role      │
                           └─────────┬───────────┘
                                     │
                            ┌────────┴────────┐
                            │ EnableRBAC?     │
                            └────────┬────────┘
                                     │
            ┌────────────────────────┴────────────────────────┐
            │                                                 │
            ▼                                                 ▼
  ┌──────────────────────┐                          ┌──────────────────────┐
  │   false (default)    │                          │   true               │
  │   role logged but    │                          │   role enforced;     │
  │   not enforced;      │                          │   denials emit a 403 │
  │   ownership lookups  │                          │   AND a durable row  │
  │   skipped entirely   │                          │   in audit_logs      │
  └──────────────────────┘                          └──────────────────────┘
```

When `tenant.enable_rbac=false` (the shipped default):

- `RequireRole` middleware logs the would-be reject and lets the
  request through (`[rbac] role insufficient (logged but not
  enforced) ...`). Use these logs to audit your role assignments
  *before* flipping the flag.
- `RequireOwnershipOrRole` short-circuits before even running its
  creator-lookup closure, so the dormant rollout window incurs zero
  extra DB roundtrips on hot mutation paths.
- `audit_logs` only records member-management events
  (`rbac.member_added` etc.). Access-denied rows start landing only
  when enforcement is on.

When `tenant.enable_rbac=true`:

- Insufficient role → HTTP 403 + a durable
  `rbac.access_denied` row in `audit_logs` (subject to a 1-minute
  sliding-window dedup so probing clients can't fill the table).
- Ownership lookup runs and decides whether the caller is the
  resource creator. Genuine "row missing" (404) is surfaced as 404,
  not 403, so client diagnostics still work.

## Configuration

YAML (`config/config.yaml`):

```yaml
tenant:
  # Default false. Flip to true once role assignments have been audited.
  enable_rbac: false
  # Optional: leaves the existing cross-tenant superuser flag in place.
  enable_cross_tenant_access: false

auth:
  # self_serve (default) — anyone may register; new tenant + Owner
  #                       membership auto-created.
  # invite_only          — public registration is rejected; new users
  #                       enter only via /tenants/:id/members invitations.
  registration_mode: self_serve

audit:
  # Days of audit history retained. A daily background sweep deletes
  # rows older than this. Default 90 (set automatically when the
  # `audit:` section is omitted from the YAML); set to 0 to disable
  # the purge entirely (the table grows monotonically).
  retention_days: 90
```

Environment overrides (always win over YAML):

| Env var                              | YAML key                       | Values                       |
|--------------------------------------|--------------------------------|------------------------------|
| `WEKNORA_TENANT_ENABLE_RBAC`         | `tenant.enable_rbac`           | `true` / `false`             |
| `WEKNORA_AUTH_REGISTRATION_MODE`     | `auth.registration_mode`       | `self_serve` / `invite_only` |
| `WEKNORA_AUDIT_RETENTION_DAYS`       | `audit.retention_days`         | non-negative integer         |

The startup logger emits one line summarising both effective values
plus their override sources, so you can confirm at boot which mode
the deployment is in.

## Schema reference

### `tenant_members`

```sql
CREATE TABLE tenant_members (
    id          BIGSERIAL PRIMARY KEY,
    user_id     VARCHAR(36) NOT NULL,
    tenant_id   BIGINT      NOT NULL,
    role        VARCHAR(32) NOT NULL,  -- owner | admin | contributor | viewer
    status      VARCHAR(32) NOT NULL DEFAULT 'active',
    joined_at   TIMESTAMP WITH TIME ZONE,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_tenant_members_user_tenant_unique
    ON tenant_members(user_id, tenant_id) WHERE status = 'active';
```

A user belongs to a tenant by having an `active` row in this table.
The `(user_id, tenant_id)` uniqueness is conditional on `status =
'active'` so historical revoked rows do not block a re-invite.

### Per-resource ownership

Migration 000043 added two columns the role check leans on:

- `knowledge_bases.creator_id VARCHAR(36)` — backfilled to the tenant
  Owner for legacy rows. Empty string / NULL means "tenant-owned, no
  human creator" and only role ≥ min may mutate.
- `custom_agents.runnable_by_viewer BOOLEAN` — when true (default),
  Viewers can run the agent in chat without a role bump.

Sub-resources resolve up the ownership chain:

```
chunk_id ─► knowledge_id ─► kb_id ─► knowledge_bases.creator_id
```

Same for FAQ entries, generated questions, tags, and wiki pages.

### `audit_logs`

```sql
CREATE TABLE audit_logs (
    id              BIGSERIAL PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL,
    actor_user_id   VARCHAR(36)  NOT NULL DEFAULT '',
    actor_role      VARCHAR(32)  NOT NULL DEFAULT '',
    action          VARCHAR(64)  NOT NULL,
    target_type     VARCHAR(32)  NOT NULL DEFAULT '',
    target_id       VARCHAR(64)  NOT NULL DEFAULT '',
    target_user_id  VARCHAR(36)  NOT NULL DEFAULT '',
    request_path    VARCHAR(512) NOT NULL DEFAULT '',
    request_method  VARCHAR(16)  NOT NULL DEFAULT '',
    outcome         VARCHAR(16)  NOT NULL DEFAULT 'success',
    details         JSONB        NOT NULL DEFAULT '{}'::JSONB,
    created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
```

Built-in actions today:

| Action                       | Outcome              | When |
|------------------------------|----------------------|------|
| `rbac.member_added`          | success              | `POST /tenants/:id/members` succeeds |
| `rbac.member_removed`        | success              | `DELETE /tenants/:id/members/:user_id` succeeds |
| `rbac.member_role_changed`   | success              | `PUT /tenants/:id/members/:user_id` succeeds |
| `rbac.member_left`           | success              | `POST /tenants/:id/members/leave` succeeds |
| `rbac.access_denied`         | denied               | `RequireRole` / `RequireOwnershipOrRole` rejects (only when enforcement is on) |

The schema is intentionally generic so future PRs can add KB / agent /
chunk action constants without another migration.

A daily background goroutine (`AuditLogRetentionRunner`, `service/`)
sweeps rows older than `audit.retention_days`. The first sweep fires
~10 minutes after boot to stay out of the way of startup traffic; the
loop then runs every 24 h. The runner short-circuits when retention
is `0`, so disabling it costs zero DB round-trips.

## Route guards

Centralised in `internal/router/rbac.go` as `rbacGuards`:

| Guard                                    | What it requires |
|------------------------------------------|------------------|
| `g.Viewer()`                             | Tenant member, any role |
| `g.Contributor()`                        | role ≥ Contributor |
| `g.Admin()`                              | role ≥ Admin |
| `g.Owner()`                              | role = Owner |
| `g.OwnedKBOrAdmin()`                     | KB.creator_id == caller, OR Admin+ |
| `g.OwnedKBOrAdminFromKbIDParam()`        | Same, but reads `:kb_id` from a non-`:id` param |
| `g.OwnedAgentOrAdmin()`                  | CustomAgent.creator_id == caller, OR Admin+ |
| `g.OwnedKnowledgeKBOrAdmin()`            | Resolves `:knowledge_id` → KB.creator_id |
| `g.OwnedChunkKBOrAdmin()`                | Resolves `:chunk_id` → knowledge → KB.creator_id |
| `g.OwnedChunkKBOrAdminFromChunkID()`     | Same chain, but starts from a different param name |
| `g.OwnedWikiKBOrAdmin()`                 | Resolves wiki page → KB.creator_id |
| `g.PathTenantMatch()`                    | URL `:tenant_id` matches the auth context |
| `g.CrossTenant()`                        | Caller has cross-tenant access |

Read-only endpoints stay on `g.Viewer()`. Anything that mutates a
shared infrastructure resource (Ollama install, parser/storage check,
WeKnora Cloud credentials, web-search providers, vector stores,
chat-history config) goes on `g.Admin()`. Anything per-resource picks
the matching `Owned*OrAdmin` guard.

## Rollout playbook

The same playbook a self-host operator (or the Tencent/WeKnora repo
itself) should follow when promoting a deployment from "logged" to
"enforced":

1. **Upgrade.** `tenant.enable_rbac=false` (default) → schema lands,
   `tenant_members` is backfilled (one Owner per tenant, the rest
   Contributor), `creator_id` populated on every KB. No behaviour
   change yet.
2. **Audit the membership.** Call
   `GET /api/v1/tenants/:id/members` (Admin+) and confirm:
   - Exactly one Owner per tenant.
   - The right humans are Contributors, Viewers, etc. (matters most
     in tenants where many users share an API key today and got
     auto-promoted to Contributor by the backfill).
   - Adjust with `PUT /api/v1/tenants/:id/members/:user_id` /
     `DELETE` as needed. Every change writes an `audit_logs` row.
3. **Watch the dormant logs.** Tail the API logs for `[rbac] role
   insufficient (logged but not enforced)` lines. These tell you
   exactly which production calls *would* 403 if you flipped the
   flag now. Fix any user role that produces unexpected denials.
4. **Flip the flag.** Set `tenant.enable_rbac=true` (or
   `WEKNORA_TENANT_ENABLE_RBAC=true`). Restart the app. From this
   point on:
   - Insufficient role → 403.
   - `audit_logs` records every reject (subject to dedup).
5. **Optional — enable invite-only.** If you've moved off self-serve
   registration, set `auth.registration_mode=invite_only`. The
   Register tab disappears from `/login` and the server rejects
   `POST /auth/register` with 403.

### Rollback

If enforcement causes unexpected breakage:

```bash
# Flip the flag back via env (no restart-with-rebuild needed):
export WEKNORA_TENANT_ENABLE_RBAC=false
# Restart the app.
```

Membership rows and `creator_id` columns stay populated so re-enabling
later doesn't require another backfill. Migration 000043 / 000044
both ship `down.sql`s, but rolling those back drops `tenant_members`
and `audit_logs` entirely — only do that if you intend to remove the
feature for good.

## Frontend behaviour

The Pinia auth store exposes:

- `authStore.currentTenantRole` — `''` until membership resolves,
  then one of the four roles. Use the empty string as a "loading"
  signal; flashing a privileged button before the role is known is
  worse UX than waiting.
- `authStore.hasRole('admin')` etc. — convenience helpers that walk
  the hierarchy.

Every mutation surface in the UI is gated either by a role check or
by a per-resource ownership predicate (`isOwner` computed off
`kb.creator_id === authStore.user?.id`). This is the matching pair
to the backend guard — when a button would 403 on click, we hide the
button instead of letting the user discover it through a failed
request.

## Common questions

### "I upgraded and now everyone is a Contributor instead of an Admin."

The backfill picks **the earliest active user in each tenant** as
that tenant's Owner; everyone else becomes Contributor. If a single
human-shared account or API key created the tenant, you may need to
demote bots and re-promote your real Admin via
`PUT /api/v1/tenants/:id/members/:user_id`.

### "I flipped the flag and a script started getting 403."

Likely the script authenticates as a Viewer / Contributor instead of
the Admin you assumed. Check the JWT user, look up the matching
`tenant_members.role`, promote if appropriate. Or — if the script
should be tenant-wide — switch it to authenticate via `X-API-Key`,
which is pinned to Admin.

### "Why is the audit log not catching some 403s?"

Two reasons:

- Sliding-window dedup. The same `(actor, path, action)` tuple
  writes at most one durable row per minute. The full reject series
  is still in the perishable application log
  (`[rbac] role insufficient ...`).
- Enforcement off. `rbac.access_denied` only writes when
  `tenant.enable_rbac=true`. The dormant mode just emits the warning
  log; member-management events do still write durably.

### "Can I have per-route ACL more granular than role + creator?"

Not in v1. The matrix is intentionally a small fixed lattice
(Viewer < Contributor < Admin < Owner) with one ownership escape
hatch per resource. Anything finer (e.g. "viewer can see audit log
for their own actions") is a follow-up.

### "Where do I see the audit log in the UI?"

`Settings → Members → Audit Log` tab (Admin+ only). Cursor-paginated
chronological feed with action / outcome chips. Filter UI is a v2.

## Testing & observability

- `make test` covers `internal/middleware/rbac_test.go`,
  `internal/handler/rbac_lookups_test.go`,
  `internal/application/service/audit_log_test.go` and
  `internal/middleware/rbac_audit_test.go` (~25 cases).
- e2e smoke under both flag values is documented in #1303 PR
  series. The matrix that ships green: each role × each guarded
  route → expected status code.
- Langfuse / OTel spans carry the resolved `TenantRole` and
  `TenantID`, so a denied request shows up as a single trace with
  the role on it — no need to correlate logs and traces by hand.
