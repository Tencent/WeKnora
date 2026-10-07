# anydoc-go (vendored)

Go bindings for [anydoc](https://github.com/firecrawl/anydoc), the Rust library
that converts Word, PowerPoint, Excel, OpenDocument, RTF, EPUB, CSV, and PDF
documents to GitHub-Flavored Markdown. WeKnora links them through cgo so office
documents can be parsed inside the Go process, without the Python docreader
service.

## Why this is vendored

The bindings come from the open pull request
[firecrawl/anydoc#30](https://github.com/firecrawl/anydoc/pull/30). Until it
merges and upstream tags `go/vX.Y.Z`, there is no module to `go get`: the PR
also expects a maintainer-only workflow to commit per-platform archives that do
not exist yet. So the binding source lives here and `go.mod` points at it:

```
replace github.com/firecrawl/anydoc/go => ./third_party/anydoc-go
```

When an upstream module is published, first verify that it includes the local
safety fixes, the 0.2.x model/error additions, and the asset-link rendering API
listed below. Only then replace this directory and the `replace` directive.
WeKnora's backend calls our additional `ToDocumentWithAssetLinks` API, so the
original PR cannot replace this fork unchanged.

## Provenance

| | |
| --- | --- |
| Upstream PR | firecrawl/anydoc#30 ("feat: add Go bindings") |
| PR head | `1a7a6c0` |
| Rebased onto | `4e3089b` (`chore: release v0.1.8`) |
| anydoc crate | `0.2.4` (from crates.io, pinned with `=`) |
| Upstream release commit | `42bf1c5ecdde9eb0d96d6bd75a9e6698cf93b14c` |
| Last upstream check | 2026-09-29: PR #30 is open, unmerged, and conflicting; head unchanged |
| License | MIT (see LICENSE) |

The PR branched before anydoc 0.1.7, so it was rebased onto v0.1.8 before
vendoring. The rebase conflicts were all release bookkeeping — the workspace
member list, the README binding sections, and the version-agreement script,
which the wasm bindings had touched in the meantime — plus the binding's own
version, bumped from 0.1.3 to 0.1.8. Taking v0.1.8 also picks up the
`pdf-inspector` 0.1.8 bump that fixes [RUSTSEC-2026-0187](https://rustsec.org/advisories/RUSTSEC-2026-0187.html),
the `lopdf` stack overflow that aborts the process on a hostile PDF.

The pin then moved to 0.1.9, which is that same binding source against a newer
PDF stack: anydoc's `src/` is byte-identical between the two releases, so the C
ABI, the document model, and the GFM serializer this binding reaches into are
unchanged, and none of the local modifications below had to be re-applied. What
0.1.9 does change is `pdf-inspector`, from the crate published as 0.1.8 to
1.14.2 (the jump in the number is that project unifying its Rust, Python, and
Node versions, not fourteen major releases). See "Why 0.1.9 matters here" below.

## Current upgrade: 0.2.4

The [0.1.9 to 0.2.4 source diff](https://github.com/firecrawl/anydoc/compare/v0.1.9...v0.2.4)
changes the document model and parsing behavior:

- 0.2.0 replaces calamine with native XLS/XLSX/XLSB readers and improves number
  formatting, merged cells, DOCX whitespace, EPUB repeated spine parts, and
  Markdown escaping.
- 0.2.1/0.2.2 preserve equations as inline/block LaTeX and refine dollar escaping.
- 0.2.3 adds inline checkboxes and spreadsheet form controls. Task-list state
  moves from `ListItem.checked` to `Inline::Checkbox`.
- 0.2.4 returns `NeedsOcr` for PDFs containing scanned pages, including mixed
  PDFs that previously returned incomplete text. The Go ABI now has a distinct
  `needs_ocr` error; page numbers remain in `ConvertError.Detail`. `NeedsOCR()`
  lets WeKnora recognize wrapped errors without matching English messages.

Node/Python/CLI hosted OCR is outside the Rust crate. WeKnora continues using
its existing builtin docreader fallback and does not send documents to Firecrawl.
The PDF default engine remains builtin. See [upgrade verification and benchmarks](UPGRADE-0.2.4.md).

## Previous upgrade: why 0.1.9 matters here

anydoc's changelog lists 0.1.9 as a single dependency bump, which reads like
housekeeping. It is nine `pdf-inspector` fixes, and seven of them are
denial-of-service bounds on PDF parsing.

Those seven bound work that had no bound at all: Form XObject expansion per
page, CID `/W` ranges, Encoding `cidrange` and ToUnicode `bfrange` expansion,
content-stream decode before operators are allocated, the detector's `Tj`/`TJ`
operand lookback, and disjoint-rect table clustering. Every one sits on the path
`pdf_inspector::process_pdf_mem` takes — the only way anydoc converts a PDF —
and every one is driven by nothing but the bytes of the uploaded file.

This is the class of bug the `guarded()` panic catcher explicitly cannot
contain, for the same reason `RUSTSEC-2026-0187` could not be: unbounded CPU
never panics, and an allocation failure aborts. The detector lookback is the
cheapest to demonstrate — before 1.14.2, each `Tj`/`TJ` whose operand was
missing rescanned the whole content stream, so a stream of bare `] TJ` tokens
was quadratic:

| `] TJ` tokens | PDF size | 0.1.8 | 0.1.9 |
| --- | --- | --- | --- |
| 40,000 | 195 KB | 1.0 s | 2.0 ms |
| 120,000 | 586 KB | 9.7 s | 4.1 ms |
| 200,000 | 977 KB | 25.9 s | 6.5 ms |
| 400,000 | 1.9 MB | 105.6 s | 12.2 ms |

One run on a 4-core VM, same Go code either side, only the linked archive
differing. Quadrupling the input cost 16× the time before the fix and roughly
4× after. A 2 MB upload holding a core for nearly two minutes needs no
privilege and no malformed container — the file parses fine, it just takes
forever, and every concurrent upload takes a core of its own.
`TestDetectorLookbackStaysLinear` in `internal/infrastructure/docparser/anydoc`
fails if a bump reintroduces it.

Two commits improve extraction rather than bound it, and both change what gets
indexed: text inside Form XObjects now tracks the text line matrix and honors
`T*`, `TL`, `'`, `"`, `Tc`, and `Tw`, so nested form text keeps its line breaks
and spacing instead of running together, and small-caps runs merge instead of
being read as extra table columns — a spurious column reaches the model as a
misaligned markdown table.

Nothing else in the range reaches WeKnora, which is worth recording so the next
upgrade does not go looking for it. The crate published as 0.1.8 was cut at
`pdf-inspector`'s `packages-2026-08-10` tag, one release behind 1.14.1, so 0.1.9
formally spans that release too — but its two functional commits both miss this
binding. One only touches the Node and Python surfaces. The other serves
invisible (`Tr 3`) OCR text layers instead of reporting `needs_ocr`, which
sounds like it would spare WeKnora an OCR round trip, except that it landed in
`extract_text_in_regions`; anydoc uses the positioned-text path, which already
retried with invisible text included. Scanned PDFs still come back as "OCR is
required" and still fall back to the docreader.

## Local modifications

Keep this list current: it is the diff a future upgrade has to re-apply. Items
2–4 are bugs in the upstream PR and are worth sending back to it.

1. `Cargo.toml` — depends on the published `anydoc = "=0.2.4"` crate instead of
   the workspace path dependency, declares its own empty `[workspace]`, and
   repeats the upstream release profile (`lto`, `strip`), which it would
   otherwise inherit from the anydoc workspace.
2. `anydoc.go` — every ABI call runs inside `call()`, which pins the goroutine
   with `runtime.LockOSThread` for the duration. The ABI reports the error
   message through a thread-local slot that a *second* call
   (`anydoc_last_error`) reads, and Go may resume a goroutine on a different OS
   thread once a cgo call returns: without pinning, a failed conversion reports
   an empty message, or one belonging to another document parsed on the thread
   it landed on. Reproduced at roughly 1 in 1500 concurrent conversions; the
   regression test is `TestErrorDetailSurvivesConcurrency` in
   `internal/infrastructure/docparser/anydoc`.
3. `model.go` — decoder preallocations are bounded by the bytes left in the
   buffer (`capFor`), and `need` rejects a negative length. A count taken
   straight from the buffer is only trustworthy while the Rust encoder and this
   decoder agree; on a skew, `make([]Block, 0, n)` would exhaust memory before
   the first bounds check, turning a version mismatch into a dead process.
4. `src/lib.rs` — conversion entry points run inside `guarded()`,
   which catches a panic and reports it as a malformed document. A panic
   escaping an `extern "C"` function aborts the process, and WeKnora parses
   untrusted uploads in the same process that serves the API. Note the limit:
   this cannot contain a stack overflow or an allocation failure, which is why
   the dependency pin below matters as much as the guard.
5. Removed the upstream CLI (`cmd/anydoc`) and the binding test suite, which
   reads fixtures from the anydoc repository. WeKnora's own tests live in
   `internal/infrastructure/docparser/anydoc`.
6. `scripts/build-anydoc-lib.sh` copies the pinned anydoc release to
   `patched-anydoc/` (gitignored) and re-exports `document_to_markdown`, which
   is crate-private in the published crate. It reads the version from the
   `anydoc = "=X.Y.Z"` pin above and checks it against `version.go`, so a
   dependency bump cannot silently leave the reported version behind.
   `src/asset_links.rs` then rewrites `ImageSource::Asset`
   to `External("images/image-N.ext")` so the official serializer emits in-place
   image links. `anydoc_to_markdown_with_asset_links` is the ABI for that path.
7. `src/model.rs`, `model.go`, and the generated header carry block/inline math
   and inline checkbox nodes. Existing wire tags are unchanged. The legacy
   `ListItem.Checked` slot is retained as nil for compatibility; callers read
   `Inline.Checked`. Asset traversal accepts math blocks. Heading text retains
   math and checkbox content when assigning image sections.
8. `src/lib.rs` and `errors.go` add `ERR_NEEDS_OCR` / `needs_ocr` and `NeedsOCR()`.
   Page numbers are preserved in the detail string; structured `pages` and
   `page_count` fields are not exposed by this Go ABI.
9. `anydoc_to_document_with_asset_links` / `ToDocumentWithAssetLinks` parse once,
   encode the original document (preserving asset IDs), then rewrite image
   sources and append the official Markdown to the same owned buffer. The
   backend uses this combined API to avoid parsing each uploaded document twice.
   The standalone APIs remain available. Corpus tests compare both paths for
   identical document trees and Markdown; all allocations use the existing
   `anydoc_buffer_free` ownership contract.

## Dependency pinning and audit

`Cargo.lock` is committed and `scripts/build-anydoc-lib.sh` builds with
`--locked`, so the archive is always the audited dependency tree. CI runs
`cargo audit` against it, because the crate that fails here is the one parsing
untrusted uploads inside the API process.

That matters concretely: with `lopdf` 0.41 — what the upstream PR's own
lockfile resolved to — a ~100 KB PDF holding a deeply nested catalog array
kills the process with a stack overflow (`RUSTSEC-2026-0187`), which neither
`guarded()` nor Go's `recover` can contain. anydoc 0.1.8 moved to `lopdf` 0.42
and the same input comes back as an ordinary error;
`TestDeeplyNestedPDFFailsWithoutKillingTheProcess` keeps it that way.

Moving the pin to 0.1.9 added no crate and removed none: `lopdf` stays at 0.42
and the whole tree below `pdf-inspector` is unchanged, so the lockfile diff is
three version lines and one checksum. That was the 0.1.9 dependency-only update;
the 0.2.4 changes are described below.

The 0.2.4 lockfile removes calamine and five exclusive dependencies, retains
`pdf-inspector` 1.14.2 and `lopdf` 0.42.0, and updates `chacha20` from the yanked
0.10.1 to 0.10.2. The latter fixes an SSE4.1 intrinsic used by the SSE2 backend
([upstream fix](https://github.com/RustCrypto/stream-ciphers/pull/580)); other
transitive versions are retained.

`cargo audit` on 2026-09-29 reports no vulnerabilities and one allowed warning:
`ttf-parser` 0.25.1 is
unmaintained (`RUSTSEC-2026-0192`), pulled in transitively by the PDF stack. It
is not a vulnerability and nothing here can fix it, so warnings report without
failing the job.

## Known upstream limitation

Markdown rendering drops embedded images: `ImageSource::Asset` renders as its
alt text, and the bytes are only reachable through the document model. The
renderer (`document_to_markdown`) is also crate-private. WeKnora keeps that
serializer: `scripts/build-anydoc-lib.sh` re-exports the one function, then
`anydoc_to_markdown_with_asset_links` rewrites `Asset` images to
`ImageSource::External("images/image-N.ext")` so the official GFM output
places them in reading order. PDF has no document model; PDFs containing scanned
pages fall back to the builtin docreader so they can be rasterized for OCR.

## Building the archive

cgo links `lib/<platform>/libanydoc_go.a`, which is a build artifact (~30 MB)
and is therefore git-ignored rather than committed:

```bash
scripts/build-anydoc-lib.sh                          # host platform
TARGET=aarch64-unknown-linux-musl scripts/build-anydoc-lib.sh
```

Then build WeKnora with the engine linked in:

```bash
make build-anydoc          # or: go build -tags anydoc ./cmd/server
```

Builds without the `anydoc` tag need no Rust toolchain and no archive; the
engine simply reports itself as unavailable.
