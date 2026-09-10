import type { SkillCatalogItem, SkillCatalogInstall } from '@/api/skill'

export function isSkillInstallOutdated(item: Pick<SkillCatalogItem, 'bundle_sha256' | 'version'>, install: Pick<SkillCatalogInstall, 'bundle_sha256' | 'version'>): boolean {
  // Content is authoritative: publishers may reuse a version label.
  if (item.bundle_sha256 && install.bundle_sha256) return item.bundle_sha256 !== install.bundle_sha256
  return Boolean(item.version && install.version && item.version !== install.version)
}
