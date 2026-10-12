export function shouldRenderHostProjectSettings(supported: boolean): boolean {
  return supported === true
}

export function withOptionalProjectDir<T extends Record<string, unknown>>(
  data: T,
  projectDir?: string | null,
): T & { project_dir?: string } {
  const trimmed = typeof projectDir === 'string' ? projectDir.trim() : ''
  const next: Record<string, unknown> = { ...data }
  delete next.project_dir
  if (trimmed) next.project_dir = trimmed
  return next as T & { project_dir?: string }
}

export function projectDirBasename(dir: string): string {
  const trimmed = dir.replace(/[\\/]+$/, '').trim()
  if (!trimmed) return dir
  const parts = trimmed.split(/[/\\]/).filter(Boolean)
  return parts[parts.length - 1] || dir
}

export function hostWorkspaceHeaderText(
  hostWorkspaceDir: string | null | undefined,
  temporaryLabel: string,
): string {
  const trimmed = hostWorkspaceDir?.trim() ?? ''
  if (!trimmed) return temporaryLabel
  return projectDirBasename(trimmed)
}
