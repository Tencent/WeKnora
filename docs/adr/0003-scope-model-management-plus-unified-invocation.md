# Refactor scope: model management + unified invocation layer, without context governance

This refactor delivers model management (capability declarations, parameter
sharding, thinking levels, catalog prefill, custom vendors) together with the
unified invocation layer (5.11: `chat.Chat` formalization, provider processor
BuildRequest/Invoke/ParseResponse contract, Anthropic folded into the adapter
system). Context budget modeling (5.3), compression rework (5.8), and usage
display / manual compaction (5.9) are explicitly out and deferred.

Why the two halves ship together:

1. **Levels must reach the wire.** `ChatOptions.Thinking` is a `*bool` today;
   without the invocation-layer change, agent-level and session-level thinking
   level overrides have no carrier — the priority chain (model → agent →
   session) collapses to model-level only, and the agent editor would expose a
   control that cannot take effect.
2. **Custom Anthropic-compatible vendor depends on it.** Folding Anthropic's
   independent code path (`anthropic.go`) into the adapter system is the stated
   precondition of the custom-Anthropic vendor; without it, 5.7 delivers only
   half its long-tail coverage.
3. **One convergence point for outbound parameters.** The unified
   BuildRequest/ShapeRequest contract is where wire-level parameter semantics
   converge. This structurally prevents the dual-field class of incidents (e.g.
   agent engine setting both `max_tokens` and `max_completion_tokens`, only one
   adapter normalizing, Ark rejecting with 400): outbound request shaping
   happens once per provider processor instead of ad-hoc patches per caller.
4. **Re-entry cost.** The invocation chain is the single path for agent, KB QA,
   and background tasks. Deferring it means reopening the same core files in
   the context-governance phase and re-running full regression twice.

Why context governance can be cleanly excluded: `context_window` /
`max_output_tokens` already exist in `ModelParameters` and are already consumed
by the agent compaction path (`AgentMaxContextTokens`). Model management only
stores and prefills these values; the deferred consumers (token-budget
compression for plain sessions, usage SSE, manual compaction) couple solely
through the stored number, so nothing in scope degrades. `UsageReporting` is
still declared in `CommonCaps` at near-zero cost even though its consumer
(5.3.1 estimator calibration) is deferred.

Risk handling: 5.11 is the medium-risk item (behavior-layer compatibility);
sequence it after the low-risk capability-declaration work per section 8
("low risk first").
