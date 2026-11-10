/** True when nginx's skill-upload location (256MB) applies, not the knowledge 50MB cap. */
export function isSkillBundleUploadUrl(url: string | undefined): boolean {
  if (!url) return false
  const path = url.split('?')[0].replace(/\/+$/, '')
  return /(?:^|\/)api\/v1\/skills\/catalog$/.test(path)
    || /(?:^|\/)api\/v1\/sandbox-configs\/[^/]+\/skills$/.test(path)
    || /(?:^|\/)skills\/catalog$/.test(path)
    || /(?:^|\/)sandbox-configs\/[^/]+\/skills$/.test(path)
}
