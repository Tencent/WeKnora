import type { Router } from 'vue-router'
import type { Composer } from 'vue-i18n'

/**
 * A single command that can be searched and invoked from the palette.
 * Kept intentionally minimal — commands are "just do something now", not
 * entities with detail pages.
 */
export interface CmdkCommand {
  id: string
  /** Localized display label. */
  label: string
  /** TDesign icon name rendered on the left. */
  icon: string
  /** Extra tokens used purely for fuzzy matching (aliases, synonyms). */
  keywords?: string[]
  /** Executed on primary action. Should close the palette itself if needed. */
  run: () => void
}

export interface CommandContext {
  router: Router
  t: Composer['t']
  /** Closes the palette; typically wires to commandPaletteStore.closePalette. */
  close: () => void
}

/**
 * Build the flat command list. Commands are intentionally static — dynamic
 * entities (KBs / agents / sessions) live in their own result groups.
 */
export function buildCommands(ctx: CommandContext): CmdkCommand[] {
  const { router, t, close } = ctx
  return [
    {
      id: 'new-chat',
      label: t('commandPalette.quick.newChat'),
      icon: 'chat-add',
      keywords: ['new', 'chat', 'conversation', '新建', '对话', 'создать'],
      run: () => {
        close()
        router.push('/platform/creatChat')
      },
    },
    {
      id: 'open-kb-list',
      label: t('commandPalette.quick.knowledgeBases'),
      icon: 'folder',
      keywords: ['kb', 'knowledge', 'base', '知识库', '文档'],
      run: () => {
        close()
        router.push('/platform/knowledge-bases')
      },
    },
    {
      id: 'open-agents',
      label: t('commandPalette.quick.agents'),
      icon: 'user-circle',
      keywords: ['agent', 'bot', '智能体', '助手'],
      run: () => {
        close()
        router.push('/platform/agents')
      },
    },
    {
      id: 'open-organizations',
      label: t('commandPalette.quick.organizations'),
      icon: 'usergroup',
      keywords: ['org', 'organization', 'team', 'space', '组织', '共享'],
      run: () => {
        close()
        router.push('/platform/organizations')
      },
    },
    {
      id: 'open-settings',
      label: t('commandPalette.quick.settings'),
      icon: 'setting',
      keywords: ['settings', 'preferences', 'config', '设置', '配置'],
      run: () => {
        close()
        router.push('/platform/settings')
      },
    },
  ]
}

/**
 * Filter commands by a free-text query. Matches on label OR any keyword,
 * case-insensitive. Empty query returns the full list unchanged so it can
 * double as a "default" empty-state listing.
 */
export function filterCommands(commands: CmdkCommand[], query: string): CmdkCommand[] {
  const q = (query || '').trim().toLowerCase()
  if (!q) return commands
  return commands.filter((cmd) => {
    if (cmd.label.toLowerCase().includes(q)) return true
    if (cmd.keywords) {
      for (const kw of cmd.keywords) {
        if (kw.toLowerCase().includes(q)) return true
      }
    }
    return false
  })
}
