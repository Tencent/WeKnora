export type SkillInstallProgressTarget = {
  configId: string
  skillId: string
}

type InstallProgressEvent = {
  percent: number
  done: boolean
}

export function progressKey(configId: string, skillId: string): string {
  return `${configId}:${skillId}`
}

export function clampInstallPercent(percent: unknown): number | null {
  if (typeof percent !== 'number' || !Number.isFinite(percent)) return null
  return Math.max(0, Math.min(100, Math.round(percent)))
}

// A stream without Redis closes immediately with done=true and percent=0.
// That is not a real progress reading, so the catalog row keeps "安装中".
export function liveInstallPercent(
  event: InstallProgressEvent | undefined,
): number | null {
  if (!event) return null
  const pct = clampInstallPercent(event.percent)
  if (pct == null) return null
  if (event.done && pct <= 0) return null
  return pct
}

export function formatBusyInstallStatus(label: string, percent: number | null): string {
  if (percent == null) return label
  return `${label} · ${percent}%`
}
