// Shared content renderer for the post-login and post-tenant-switch
// NotifyPlugin cards. Both cards present the same shape ("you are in
// <workspace> as <role>"), so we render them with a unified visual
// language to keep the two surfaces consistent.
//
// Design choices:
//
//   * The workspace name is the primary anchor of the sentence, so it
//     is rendered as plain bold text (no chip / background) — the
//     surrounding sentence already frames it and another box around
//     would be double-emphasis.
//   * The role is a categorical attribute and benefits from a visible
//     coloured tag, so it goes through TDesign's <t-tag> with the same
//     role→theme mapping used by TenantMembers (settings). This keeps
//     "what an owner / admin / contributor / viewer looks like"
//     consistent across the product instead of inventing a parallel
//     chip palette.
//   * No forced wrapping — let the Notify width drive line breaks
//     naturally so the sentence reads as one phrase whenever it fits.
//
// The renderer interpolates a template string carrying `{name}` and
// optionally `{role}` placeholders. Anything around them is rendered
// verbatim so translators can reorder the sentence per locale.

import { h, type VNode } from 'vue'
import { Tag as TTag, Icon as TIcon } from 'tdesign-vue-next'

// Mirror TenantMembers.roleTagTheme() so the "owner is blue, admin is
// orange, ..." identity stays consistent across surfaces. If that map
// changes there, change it here too (or, longer term, lift both into
// useRoleLabel).
type TagTheme = 'primary' | 'warning' | 'success' | 'default'

const ROLE_THEME: Record<string, TagTheme> = {
  owner: 'primary',
  admin: 'warning',
  contributor: 'success',
}

function roleTagTheme(roleEnum: string | undefined): TagTheme {
  return (roleEnum && ROLE_THEME[roleEnum]) || 'default'
}

export interface WorkspaceNotifyContentOptions {
  /**
   * The i18n-translated template carrying `{name}` and optionally
   * `{role}` placeholders. Anything around the placeholders is rendered
   * as plain text in the output, in order, so the sentence reads
   * naturally in every locale. Pass the message via `tm()` (not `t()`)
   * so the placeholders survive without interpolation.
   */
  template: string
  /** Workspace display name. Rendered as bold inline text. */
  name: string
  /** Human-readable role label, e.g. "所有者" / "Owner". Omit for the no-role variant. */
  roleLabel?: string
  /** Raw role enum value, e.g. "owner". Drives the tag theme colour. */
  roleEnum?: string
  /**
   * Icon name (TDesign icon) for the role chip. Pass from
   * `useRoleLabel().roleIcon(roleEnum)`. Empty / undefined renders the
   * tag without a leading icon.
   */
  roleIconName?: string
}

/**
 * Build a NotifyPlugin `content` factory rendering the workspace name
 * in bold and the role as a TDesign Tag. Returns a `() => VNode` so
 * TDesign re-invokes it per render, matching the plugin's TNode
 * contract.
 */
export function renderWorkspaceNotifyContent(
  opts: WorkspaceNotifyContentOptions,
): () => VNode {
  return () => {
    const tokens = opts.template.split(/(\{name\}|\{role\})/g)
    const parts: VNode[] = []
    for (const tok of tokens) {
      if (tok === '{name}') {
        parts.push(
          h(
            'strong',
            { style: { fontWeight: '600', color: 'var(--td-text-color-primary)' } },
            opts.name,
          ),
        )
      } else if (tok === '{role}') {
        if (opts.roleLabel) {
          const slots: Record<string, () => VNode | string | undefined> = {
            default: () => opts.roleLabel,
          }
          if (opts.roleIconName) {
            slots.icon = () => h(TIcon, { name: opts.roleIconName, size: '12px' })
          }
          parts.push(
            h(
              TTag,
              {
                theme: roleTagTheme(opts.roleEnum),
                size: 'small',
                variant: 'light',
                style: { verticalAlign: 'middle', marginInline: '2px' },
              },
              slots,
            ),
          )
        }
        // If template has {role} but no label was passed, drop the
        // marker silently — caller should pick the no-role template
        // instead, but this guards against the raw "{role}" leaking
        // through if they forget.
      } else if (tok) {
        parts.push(h('span', tok))
      }
    }
    return h(
      'span',
      { style: { lineHeight: '1.6', color: 'var(--td-text-color-secondary)' } },
      parts,
    )
  }
}
