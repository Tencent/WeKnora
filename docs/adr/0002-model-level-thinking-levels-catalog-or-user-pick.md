# Model-level thinking levels come from catalog hits or user selection, never adapter tables

For each model record, the selectable thinking-level options are the provider
adapter's level enumeration (provider-level `SupportedLevels` — vendor
knowledge, declared once in the adapter). Which options are *selected* for a
given model comes from the remote listing metadata or models.json when the
model id matches; when nothing matches, the selection is left empty and the
user picks from the enumeration (multi-select). We rejected maintaining
per-model level tables inside adapters: catalog data plus user choice covers
both mainstream and unknown models without a second hand-fed source of vendor
knowledge.

Catalog format decision (supersedes the doc's models.dev-compatibility plan):
models.json is WeKnora's **own schema**, self-defined. The upstream models.dev
api.json is a reference example only — verified across all 213 providers, it
carries only `reasoning` + `reasoning_options` and no level information, which
is why a community-compatible schema could not prefill levels anyway. Our own
format carries per-model thinking levels, context window, max output, and
input modalities as first-class fields.

Selected levels are user configuration: once saved they are final (ADR 0001).

When the user saves without picking any level, each vendor adapter decides what
to do: an adapter may declare a provider-level default level, or emit no
thinking-level parameter at all and let the upstream provider use its own
default. This is provider-level knowledge inside the adapter — distinct from
the rejected per-model tables.
