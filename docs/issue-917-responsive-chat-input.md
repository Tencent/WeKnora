# Issue 917: Responsive chat input

## Scope

This change keeps the existing chat input behavior and visual language while
removing desktop-only width assumptions that caused horizontal overflow on
phone-sized viewports.

- The chat route and platform shell can now shrink to the viewport.
- The new-chat page no longer applies fixed `translateX` offsets or hard-coded
  textarea widths at smaller breakpoints.
- The knowledge-base chat entry point uses the same container-driven sizing, so
  switching into a knowledge base does not reintroduce the old fixed offsets.
- The input container remains capped at 800px on larger screens and becomes
  container-driven below 960px.
- Mobile controls use smaller spacing and a shorter textarea range so the input
  does not dominate a phone viewport.

## Regression matrix

Before opening the PR, verify the authenticated chat route at:

| Viewport | Expected result |
| --- | --- |
| 360 × 800 | No horizontal scrollbar; input and controls stay within the viewport |
| 390 × 844 | No horizontal scrollbar; input remains centered with 12px side padding |
| 768 × 1024 | Input uses available width without fixed offsets |
| 1440 × 900 | Existing centered, max-800px desktop input is preserved |

The same four viewport checks should be captured for the new-chat route. The
screenshots belong in the PR description so reviewers can compare the narrow
and desktop behavior directly.

## Verification commands

```bash
pnpm --dir frontend test -- src/components/responsiveChatLayout.test.mjs
pnpm --dir frontend type-check
pnpm --dir frontend build-only
```
