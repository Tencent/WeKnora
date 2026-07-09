Status: DONE

Fixes:
- Advanced WeCom incremental cursor from every fetched envelope seq before filtering, including empty `ConversationID` messages.
- Added WeCom connector error sanitization for configured `secret`, configured `private_key`, and generic private-key/PEM markers on validate and fetch errors.
- Rendered secret multiline credential fields as password inputs instead of plaintext textareas.

Tests:
- `go test ./internal/datasource/connector/wecom_chat_archive -count=1` passed.
- `npm --prefix frontend test -- src/views/knowledge/settings/datasourceFieldRendering.test.ts src/views/knowledge/settings/datasourceDefaults.test.ts` passed: 4 tests.
- `npm --prefix frontend run type-check` passed.
- `git diff --check` passed.

Commits:
- Pending at report creation.

Concerns:
- The secret multiline field now uses a single-line password input, so pasted private keys are masked but less ergonomic than a secure multiline editor.
