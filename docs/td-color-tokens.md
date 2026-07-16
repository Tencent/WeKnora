# TDesign color tokens (hardcoded → CSS variables)

WeKnora UI colors should follow TDesign theme tokens so light/dark mode and brand overrides stay consistent.

## Rule

Prefer CSS variables over raw hex / `rgba(7, 192, 95, …)` brand greens:

| Before | After |
|--------|--------|
| `#07c05f` / brand green hex | `var(--td-brand-color, #07c05f)` |
| `#00a67e` | `var(--td-brand-color-active)` |
| `rgba(7, 192, 95, α)` | `color-mix(in srgb, var(--td-brand-color) N%, transparent)` |
| Success tint greens (`rgba(0, 168, 112, …)`) | `var(--td-success-color-light)` / `color-mix(... var(--td-success-color) ...)` |

Upstream default brand remains **green** (`#07c05f`). Do not contribute a custom blue theme palette via `theme.css`.

## Scope (this PR batch)

- Knowledge base list / organization list brand tints
- Chat tool-result shared styles + Wiki / WebFetch / DocumentInfo accents
- Settings overlay / shadow tokens

## Out of scope

- Replacing the default green token values in `theme.css`
- Agent-mode special accent colors and avatar gradients
- Full-repo sweep of every remaining hardcoded color (follow-up PRs OK)
