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

export function installOutdated(catalog: SkillDigest, install: SkillDigest): boolean {
  return Boolean(
    catalog.bundle_sha256
    && install.bundle_sha256
    && catalog.bundle_sha256 !== install.bundle_sha256,
  )
}

// Only a ready install can take the catalog version: a busy one is already
// being rewritten, and a failed one is retried rather than upgraded.
// Disabled installs count: the files stay in the image either way.
export function installUpgradable(catalog: SkillDigest, install: SkillInstallDigest): boolean {
  return install.status === 'ready' && installOutdated(catalog, install)
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
