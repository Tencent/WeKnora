import type { JournalRankMetadata } from '@/types/journalRank'

export type JournalRankBadge = { key: string; label: string; tone: string }

const preferredFields: Array<[string, string, string]> = [
  ['sciif', 'IF', 'impact'],
  ['sci', 'SCI', 'quartile'],
  ['ssci', 'SSCI', 'quartile'],
  ['sciUp', '中科院', 'cas'],
  ['sciUpSmall', '中科院小类', 'cas'],
  ['sciUpTop', '中科院 Top', 'cas'],
  ['sciwarn', '中科院预警', 'warning'],
  ['pku', '北核', 'core'],
  ['cssci', 'CSSCI', 'core'],
  ['cscd', 'CSCD', 'core'],
  ['zhongguokejihexin', '科技核心', 'core'],
  ['ccf', 'CCF', 'index'],
  ['ajg', 'AJG', 'index'],
  ['ft50', 'FT50', 'index'],
  ['utd24', 'UTD24', 'index'],
  ['eii', 'EI', 'index'],
  ['ahci', 'A&HCI', 'index'],
  ['jci', 'JCI', 'impact'],
  ['sciif5', 'IF 5年', 'impact'],
  ['esi', 'ESI', 'index'],
]

const isAffirmative = (value: string) => /^(是|yes|true|1|收录|来源期刊)$/i.test(value.trim())

export function buildJournalRankBadges(rank?: JournalRankMetadata | null): JournalRankBadge[] {
  if (!rank?.found) return []

  const values = { ...(rank.official_all || {}), ...(rank.official || {}) }
  const result: JournalRankBadge[] = []
  for (const [key, prefix, tone] of preferredFields) {
    const value = String(values[key] || '').trim()
    if (!value) continue
    const label = isAffirmative(value) || value === prefix ? prefix : `${prefix} ${value}`
    result.push({ key, label, tone })
  }

  for (const [index, item] of (rank.custom || []).entries()) {
    if (!item?.label || !item?.level) continue
    result.push({ key: `custom-${index}-${item.label}`, label: `${item.label} ${item.level}`, tone: 'custom' })
  }
  return result
}
