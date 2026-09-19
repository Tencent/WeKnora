import { reactive } from 'vue'
import { installSkillCatalog, registerSkillCatalogFromSource } from '@/api/skill'
import { getConfigSkill, type ConfigSkillInstallEvent } from '@/api/system'
import { useConfigSkillInstallProgress } from './useConfigSkillInstallProgress'

/**
 * Install state for skills offered as cards in a chat.
 *
 * One skill can be offered by more than one card — a search result, and the
 * notice on a command that loaded it into the sandbox — and a card is
 * remounted whenever the steps tree folds. The state is therefore keyed by
 * what is installed where (sandbox config + source) and lives at module level,
 * so every card for the same skill shows the same progress.
 */

export type ChatSkillInstallPhase = 'registering' | 'installing' | 'ready' | 'failed'

export interface ChatSkillInstallState {
  phase: ChatSkillInstallPhase
  configId: string
  skillId?: string
  error?: string
}

const states = reactive<Record<string, ChatSkillInstallState>>({})
const keyBySkill = new Map<string, string>()

export function chatSkillInstallKey(configId: string, source: string): string {
  return `${configId}\n${source}`
}

function skillKey(configId: string, skillId: string): string {
  return `${configId}\n${skillId}`
}

async function settle(configId: string, skillId: string, event: ConfigSkillInstallEvent) {
  const key = keyBySkill.get(skillKey(configId, skillId))
  if (!key) return
  let status = event.status || ''
  let error = ''
  // The last stream event does not always carry the outcome; the row does.
  try {
    const res = await getConfigSkill(configId, skillId)
    status = res?.data?.status || status
    error = res?.data?.error || ''
  } catch {
    // Keep what the stream said.
  }
  if (status === 'ready') {
    states[key] = { phase: 'ready', configId, skillId }
  } else {
    states[key] = { phase: 'failed', configId, skillId, error: error || event.log || '' }
  }
}

const progress = useConfigSkillInstallProgress({
  onDone(target, event) {
    void settle(target.configId, target.skillId, event)
  },
})

export function chatSkillInstallState(configId: string, source: string): ChatSkillInstallState | undefined {
  return states[chatSkillInstallKey(configId, source)]
}

export function chatSkillInstallPercent(state: ChatSkillInstallState | undefined): number | null {
  if (!state?.skillId || state.phase !== 'installing') return null
  return progress.percentOf(state.configId, state.skillId)
}

/**
 * Registers the source in the workspace catalog, then installs that catalog
 * entry onto one sandbox config. Both calls are the admin endpoints the skill
 * settings page uses, so the server applies the same role check and the same
 * source validation to a click in chat.
 */
export async function installChatSkill(configId: string, source: string): Promise<void> {
  const key = chatSkillInstallKey(configId, source)
  const current = states[key]
  if (current && (current.phase === 'registering' || current.phase === 'installing')) return

  states[key] = { phase: 'registering', configId }
  try {
    const registered = await registerSkillCatalogFromSource(source)
    const catalogId = registered?.data?.id
    if (!catalogId) throw new Error('')
    const res = await installSkillCatalog(catalogId, [configId])
    const failure = res?.data?.errors?.[configId]
    if (failure) throw new Error(failure)
    const skillId = res?.data?.installs?.[configId]
    if (!skillId) throw new Error('')
    keyBySkill.set(skillKey(configId, skillId), key)
    states[key] = { phase: 'installing', configId, skillId }
    progress.follow(configId, skillId)
  } catch (e: any) {
    states[key] = { phase: 'failed', configId, error: e?.message || '' }
  }
}
