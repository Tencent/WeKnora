# SvgIcon (custom brand icons)

WeKnora uses `SvgIcon` for a small set of **product brand icons** (knowledge base, agent, organization, thinking, etc.). Generic UI icons (chevron, copy, search) continue to use TDesign `t-icon`.

## Why

Static green/grey asset pairs (`zhishiku.svg` / `zhishiku-green.svg`, …) cannot follow light/dark theme tokens. `SvgIcon` paths use `currentColor`; the component sets color from TDesign CSS variables.

## API

```vue
import { SvgIcon } from '@/components/icons'

<SvgIcon name="agent" theme="brand" :size="18" />
<SvgIcon name="organization" variant="green" :size="14" />
<SvgIcon name="thinking" theme="secondary" />
```

| Prop | Type | Default | Notes |
|------|------|---------|-------|
| `name` | icon registry key | required | See `registry.ts` `IconName` |
| `size` | `number \| string` | `20` | Number → px |
| `color` | `string` | — | Highest priority (CSS color / `var(...)`) |
| `variant` | `default \| green \| active \| thin \| grey` | `default` | May alias to another glyph (e.g. `agent` + `green` → `agentGreen`) |
| `theme` | `default \| brand \| secondary \| placeholder \| anti` | `default` | Maps to `--td-*` with green brand fallback `#07c05f` |

Color resolution order: `color` → variant shortcut (`green`/`grey`) → `theme` → primary text.

## Registry

Source of truth: `frontend/src/components/icons/registry.ts`.

Registered names include: `zhishiku`, `zhishikuThin`, `organization`, `ziliao`, `agent`, `agentGreen`, `agentActive`, `user`, `thinking`, `websearch`, `fileAdd`, `setting`, `prefixIcon`.

Brand fallback stays upstream green:

```ts
brand: 'var(--td-brand-color, #07c05f)'
```

## First-batch call sites

| Area | Usage |
|------|--------|
| Side menu | Active → `theme="brand"`, inactive → `theme="secondary"` |
| Agent selector detail meta | `organization` / `user` |
| Agent stream titles | `agent` / `thinking` (replaces CSS-mask assets) |
| Deep think done state | `thinking` |
| Mention chip org badge | `organization` + `green` / `grey` |

## Out of scope

- Replacing every `t-icon` in the app
- Shipping additional offline CDN guards beyond what the tree already has

## Verification

Automated:

```bash
cd frontend
node --test src/components/icons/registry.test.mjs
node --test src/views/chat/components/AgentStreamDisplay.style.test.mjs
npm run type-check
```

Manual (light + dark) — verified locally:

1. Side menu: active route icons use brand color; inactive use secondary text color.
2. Theme toggle light/dark: icons follow `--td-*` (inline SVG / `currentColor`, no stuck green/grey bitmaps).
3. Agent conversation stream: collapsed-steps root uses the agent glyph; thinking rows use the thinking glyph (SvgIcon, not CSS-mask `<img>`).
