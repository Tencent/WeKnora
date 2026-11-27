# Restrict MCP tools by IM conversation

An administrator can restrict an individual MCP tool to selected messaging
conversations without disabling it for Web, API or scheduled runs. This is an
additional restriction: it never overrides a disabled tool, a risk policy, an
approval requirement or downstream credentials.

## Configure

Use the existing authenticated MCP service management API. The same workspace
and API-key capability checks apply. Viewer members can list policies; Admin
members can replace or remove them.

```http
PUT /api/v1/mcp-services/{service_id}/tool-session-policies/{tool_name}
Content-Type: application/json
Authorization: Bearer <token>

{
  "allowed_sessions": [
    {"platform": "feishu", "chat_type": "group", "chat_id": "oc_ops_group"},
    {"platform": "slack", "chat_type": "group", "chat_id": "C012345"}
  ]
}
```

URL-encode the tool name when constructing the path. The list replaces the
previous policy atomically. An empty list denies all IM conversations. Delete
the policy to restore the original unrestricted IM behavior:

```http
DELETE /api/v1/mcp-services/{service_id}/tool-session-policies/{tool_name}
GET /api/v1/mcp-services/{service_id}/tool-session-policies
```

Each rule requires a platform (`wecom`, `feishu`, `lark`, `telegram`, `dingtalk`,
`mattermost`, `wechat`, `qqbot`, `slack` or `yunzhijia`), a chat type (`direct` or
`group`) and a chat ID. The ID is an exact match or the entire string `*`.
Platform/type wildcards and partial ID wildcards are rejected. Rules are ORed.
For a platform that supplies no private-chat ID, only an explicit `*` rule for
that platform's `direct` chats can match. A missing platform or chat type never
matches, including when an older IM context lacks conversation metadata.

The verified IM channel and normalized incoming message supply the identity;
tool arguments and caller-supplied HTTP headers cannot select it. The identity
survives detached QA work. Listing tools checks policies once per service, not
once per tool. Execution reads the current policy again, so revoking a tool
after discovery prevents a subsequent call. Policy read failures refuse IM
discovery and execution; non-IM calls do not read these policies.

Policies are stored separately from MCP approval settings. Apply migrations
before deploying the new runtime. This feature requires all replicas handling
IM messages to use the updated runtime; older binaries do not enforce it.

Unavailable tools are omitted from discovery. The model is not sent the
allowlist or conversation IDs. Explain any desired denial wording in the
agent's instructions; those instructions are presentation, not authorization.
