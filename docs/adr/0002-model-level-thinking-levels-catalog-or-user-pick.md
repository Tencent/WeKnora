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
models.json is WeKnora's **own schema**, self-defined. Correction to the
proposal's data fact: the 4.5 MB api.json snapshot the doc verified carried
only `reasoning` + `reasoning_options`; the current 6 MB upstream api.json
ALSO carries level data — `reasoning_options: [{type: "effort", values:
[low/medium/high/…]}, {type: "toggle"}, {type: "budget_tokens", min}]` — so
levels for most thinking vendors (OpenAI, DeepSeek, Volcengine, Zhipu,
Google) ARE now sourceable. The own schema is still preferred: values outside
the platform vocabulary (none/minimal) need filtering, vendor ids carry
date/-latest suffixes needing normalization, and continuous vendors
(budget_tokens) still map through adapter-private tables (design §4.3). The
embedded LOCAL seed (internal/models/catalog/data/models.json, 15 providers /
857 models) is converted from the upstream api.json with those rules applied.

Selected levels are user configuration: once saved they are final (ADR 0001).

When the user saves without picking any level, each vendor adapter decides what
to do: an adapter may declare a provider-level default level, or emit no
thinking-level parameter at all and let the upstream provider use its own
default. This is provider-level knowledge inside the adapter — distinct from
the rejected per-model tables.
