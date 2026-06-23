# `chat` and `session ask` — streaming RAG

Both stream an NDJSON event log (one JSON object per line) by default. They share
the same event vocabulary; `chat` does plain KB RAG, `session ask` invokes a
custom agent.

## Commands & flags

```
weknora chat "<query>" --kb <name-or-id> [--session <id>]
weknora session ask "<query>" --agent <agent-id> [--session <id>]
```

- `--kb` (chat) is required name-or-id. `--agent` (session ask) is required.
- `--session <id>` continues an existing conversation; omit to start a new one.
- `--format text` renders a human transcript instead of NDJSON; `--format json`
  / `--format ndjson` both produce the NDJSON stream (identical here).

## Event stream

The CLI emits an `init` line first, then passes SDK events through verbatim:

```jsonc
{"event":"init","session_id":"sess_abc","message_id":"msg_xyz","profile":"prod"}
{"response_type":"thinking","content":"…"}
{"response_type":"tool_call","tool_calls":[…]}        // agent only
{"response_type":"tool_result","content":"…"}         // agent only
{"response_type":"references","knowledge_references":[…]}
{"response_type":"answer","content":"partial text…"}   // streamed in pieces
{"response_type":"complete","done":true}
```

- Accumulate `response_type:"answer"` `content` pieces for the final answer.
- `knowledge_references` carry the grounding chunks (source attribution).
- **Keep `init.session_id` and `init.message_id`**: `session_id` continues the
  chat (`--session`); `message_id` is needed to `session stop` or
  `session continue-stream` that specific message.
- On failure mid-stream you get `response_type:"error"`; a transport/HTTP error
  surfaces as the normal error envelope on stderr with a typed code.

## Recovery

- **Stop server-side generation:** `weknora session stop <session-id> --message
  <message-id>`. Ctrl-C only closes your local connection — the server keeps
  generating (and billing) until told to stop.
- **Re-attach after a dropped connection:** `weknora session continue-stream
  <session-id> --message <message-id>`. The server replays the event log from
  index 0 then tails new events, so **dedupe by message_id** if you already
  consumed some events. Buffer TTL is ~1h (redis) or process-lifetime (memory).
