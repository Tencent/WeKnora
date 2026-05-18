// Shared content renderer for the post-login and post-tenant-switch
// NotifyPlugin cards. Both cards present the same shape ("you are in
// <workspace> as <role>"), so we render them with a unified visual
// language — a neutral chip for the workspace name and a role-coloured
// chip for the role — instead of plain interpolated text.
//
// The renderer interpolates a template string carrying `{name}` and
// optionally `{role}` placeholders. Everything around those placeholders
// is rendered verbatim, which lets translators reorder the sentence
// however their language wants without further code changes.
//
// Why not just pass html to NotifyPlugin's `content` string?
// TDesign's plugin treats `content` as text. Passing a render function
// (TNode) is the supported way to inject styled VNodes; doing it
// through a shared helper keeps the chip styles in one place rather
// than scattered across two call sites.

import { h, type VNode } from 'vue'
import { Icon as TIcon } from 'tdesign-vue-next'

// Inline styles (rather than a global stylesheet) because NotifyPlugin
// renders into a teleported overlay outside any scoped <style>. Keeping
// the CSS adjacent to the markup also makes the chip self-contained:
// callers don't need to remember to import a stylesheet.
const NAME_CHIP_STYLE: Record<string, string> = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '4px',
  padding: '1px 8px',
  borderRadius: '10px',
  background: 'var(--td-bg-color-secondarycontainer)',
  color: 'var(--td-text-color-primary)',
  fontWeight: '500',
  margin: '0 2px',
  maxWidth: '220px',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
  verticalAlign: 'middle',
}

// Role chip palettes mirror the colour intent of the role badges used
// elsewhere (TenantMembers, the permission matrix) so the user gets a
// consistent "this is what an owner / admin / contributor / viewer
// looks like" signal across the app.
const ROLE_TINTS: Record<string, { bg: string; fg: string }> = {
  owner:       { bg: 'rgba(7, 192, 95, 0.12)',   fg: '#067a45' },
  admin:       { bg: 'rgba(0, 102, 255, 0.12)',  fg: '#1a5dd0' },
  contributor: { bg: 'rgba(255, 153, 0, 0.12)',  fg: '#b46a00' },
  viewer:      { bg: 'rgba(120, 120, 120, 0.12)', fg: '#5a5a5a' },
}

const ROLE_DEFAULT_TINT = {
  bg: 'rgba(120, 120, 120, 0.12)',
  fg: 'var(--td-text-color-secondary)',
}

function roleChipStyle(roleEnum: string | undefined): Record<string, string> {
  const tint = (roleEnum && ROLE_TINTS[roleEnum]) || ROLE_DEFAULT_TINT
  return {
    ...NAME_CHIP_STYLE,
    background: tint.bg,
    color: tint.fg,
    maxWidth: '120px',
  }
}

// Use the imported TDesign Icon component directly. `h('t-icon', ...)`
// would treat the tag as a custom HTML element in render-function land
// (component name resolution is a template-compiler-only feature), so
// the icon would silently render nothing.
function icon(name: string): VNode {
  return h(TIcon, {
    name,
    size: '13px',
    style: { flexShrink: '0' },
  })
}

function nameChip(name: string): VNode {
  return h('span', { style: NAME_CHIP_STYLE, title: name }, [
    icon('application'),
    h('span', name),
  ])
}

function roleChip(label: string, roleEnum: string | undefined, iconName: string): VNode {
  return h('span', { style: roleChipStyle(roleEnum), title: label }, [
    iconName ? icon(iconName) : null,
    h('span', label),
  ])
}

export interface WorkspaceNotifyContentOptions {
  /**
   * The i18n-translated template carrying `{name}` and optionally
   * `{role}` placeholders. Anything around the placeholders is rendered
   * as plain text in the output, in order, so the sentence reads
   * naturally in every locale.
   */
  template: string
  /** Workspace display name. Wrapped in a neutral chip. */
  name: string
  /** Human-readable role label, e.g. "所有者" / "Owner". Omit for the no-role variant. */
  roleLabel?: string
  /** Raw role enum value, e.g. "owner". Drives chip colour. */
  roleEnum?: string
  /** Icon name for the role chip. Pass from useRoleLabel().roleIcon. */
  roleIconName?: string
}

/**
 * Render a NotifyPlugin `content` VNode for the workspace-context
 * cards. The returned function is suitable for the plugin's `content:
 * () => VNode` slot. Returns a `() => VNode` rather than a VNode so
 * TDesign re-invokes it on each render (matches the plugin's
 * expectation of a TNode factory).
 */
export function renderWorkspaceNotifyContent(
  opts: WorkspaceNotifyContentOptions,
): () => VNode {
  return () => {
    const parts: Array<string | VNode> = []
    // Split on the two placeholders but keep delimiters so we can
    // interleave plain text and chips in original order.
    const tokens = opts.template.split(/(\{name\}|\{role\})/g)
    for (const tok of tokens) {
      if (tok === '{name}') {
        parts.push(nameChip(opts.name))
      } else if (tok === '{role}') {
        if (opts.roleLabel) {
          parts.push(roleChip(opts.roleLabel, opts.roleEnum, opts.roleIconName || ''))
        }
        // If the template carries {role} but the caller didn't pass a
        // label, drop the marker silently — caller should normally pick
        // a no-role template instead, but this avoids leaking the raw
        // "{role}" string into the UI if they forget.
      } else if (tok) {
        parts.push(tok)
      }
    }
    return h(
      'div',
      {
        style: {
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'center',
          rowGap: '6px',
          lineHeight: '1.8',
        },
      },
      parts.map((p) => (typeof p === 'string' ? h('span', p) : p)),
    )
  }
}
