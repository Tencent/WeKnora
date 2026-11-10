# Markdown extraction fixtures

These HTML fixtures are authored for the extraction regression tests:

- `article.html`: retains sibling sections around a nested `.content` element after
  successful Readability extraction.
- `structure.html`: preserves links, lists, tables and code indentation while
  removing empty anchors and navigation noise.
- `fallback.html`: retains directory entries and resolves relative links when
  article extraction falls back to document content.

Run the fixture tests from the repository root:

```sh
go test ./internal/infrastructure/web_fetch -run TestMarkdownExtractionFixtures
```

The tests assert content and structural preservation independently of incidental
Markdown spacing and list marker choices.
