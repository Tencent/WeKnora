import type { SkillCatalogItem, SkillInfo } from '@/api/skill'

export type CatalogSkillRow = SkillCatalogItem & {
  preinstalled: boolean
  installed: boolean
  selectable: boolean
  installStatus: string
  installEnabled: boolean
}

// The runtime's usable set is authoritative. A catalog entry is an available
// package, not proof that the selected sandbox can execute it.
export function buildCatalogSkillRows(catalog: SkillCatalogItem[], usable: SkillInfo[], configId: string): CatalogSkillRow[] {
  const available = new Map(usable.map(skill => [skill.name, skill]))
  const items = [...catalog]
  const names = new Set(items.map(item => item.name))
  for (const skill of usable) {
    if (!names.has(skill.name)) {
      items.push({ ...skill, id: '', created_at: '', updated_at: '', installations: [] })
      names.add(skill.name)
    }
  }
  return items.map(item => {
    const runtime = available.get(item.name)
    const inst = item.installations.find(row => row.sandbox_config_id === configId)
    const preinstalled = runtime?.source === 'builtin'
    return {
      ...item,
      description: runtime?.description || item.description,
      version: runtime?.version || item.version,
      preinstalled,
      installed: preinstalled || (!!inst && inst.status !== 'removed'),
      selectable: !!runtime,
      installStatus: preinstalled ? 'ready' : inst?.status || '',
      installEnabled: preinstalled || !!inst?.enabled,
    }
  })
}
