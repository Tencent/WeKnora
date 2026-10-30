# Fixture provenance

`golden_test.go` embeds the response from the official
[reranking guide](https://docs.deepinfra.com/apis/reranker)
([Markdown source](https://docs.deepinfra.com/apis/reranker.md)), retrieved on
2026-10-14. The source example uses `Qwen/Qwen3-Reranker-8B`, one broadcast query,
four documents, four positional scores and `input_tokens: 348`.

The golden test repeats the query to produce equal-length input arrays, the
other request form allowed by that guide. It exercises decoding and positional
mapping against a local HTTP mock. The score values are documentation examples;
they are not an observed response from either the 8B or 4B model. No inference
API was called to create this fixture.

The [4B model API page](https://deepinfra.com/Qwen/Qwen3-Reranker-4B/api) uses the
same request URL and field shape, but its displayed example pairs one document
with a three-score response template. That template is deliberately not used
as evidence of a successful request or as the golden response.

All other responses in `client_test.go` are synthetic local fixtures for error
handling and mapping. Values outside the probability range check that the wire
client preserves numbers; they do not claim such values are produced by a
particular model. Score interpretation and model limits are provider settings,
not inferred from this fixture.
