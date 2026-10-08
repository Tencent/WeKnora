// A sandbox keeps running the archive it was built from when the catalog
// definition moves on, so an install is outdated exactly when its digest
// differs from the definition's. Rows that predate digests have nothing to
// compare against and are never reported as outdated.

interface SkillDigest {
  bundle_sha256?: string
  version?: string
}

interface SkillInstallDigest extends SkillDigest {
  status: string
}

interface SkillInstallServed {
  status: string
  served?: { version?: string }
}

type Translate = (key: string, params?: Record<string, unknown>) => string

export function installOutdated(catalog: SkillDigest, install: SkillDigest): boolean {
  return Boolean(
    catalog.bundle_sha256
    && install.bundle_sha256
    && catalog.bundle_sha256 !== install.bundle_sha256,
  )
}

// An install that differs from the catalog is moved onto the catalog version
// by upgrading, whether it is ready or failed. A retry replays the archive the
// install is pinned to, which for a failed one the catalog has already moved
// past, so it is only offered while the two agree. A busy install is already
// being rewritten. Disabled installs count: the files stay in the image either
// way.
export function installUpgradable(catalog: SkillDigest, install: SkillInstallDigest): boolean {
  return (install.status === 'ready' || install.status === 'failed') && installOutdated(catalog, install)
}

// An upgrade only replaces the image when it succeeds, so while one runs, and
// after one fails, the sandbox keeps running the previous version and the
// agent keeps using it. null when the install itself is what runs, or when
// nothing of the skill is in the image yet.
export function servedPrevious(
  install: SkillInstallServed,
): { upgrading: boolean; version: string } | null {
  if (!install.served) return null
  if (install.status !== 'installing' && install.status !== 'failed') return null
  return { upgrading: install.status === 'installing', version: (install.served.version || '').trim() }
}

export function servedPreviousText(t: Translate, install: SkillInstallServed): string {
  const served = servedPrevious(install)
  if (!served) return ''
  const key = served.upgrading ? 'settings.skills.servedWhileUpgrading' : 'settings.skills.servedAfterFailure'
  return served.version ? t(key, { version: served.version }) : t(`${key}Plain`)
}

// The version strings are optional frontmatter, so the pair is only worth
// showing when both sides name one and they differ.
export function upgradeVersions(
  catalog: SkillDigest, install: SkillDigest,
): { from: string; to: string } | null {
  const from = (install.version || '').trim()
  const to = (catalog.version || '').trim()
  return from && to && from !== to ? { from, to } : null
}
