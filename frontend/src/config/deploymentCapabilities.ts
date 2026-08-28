export const DEPLOYMENT_CAPABILITY_KEYS = [
  'organizations',
  'agents',
  'integrations.im',
  'integrations.embed',
  'integrations.api',
  'settings.mcp',
  'settings.websearch',
  'settings.vectorstore',
  'settings.storage',
  'settings.sandbox',
] as const

export type DeploymentCapabilityKey = typeof DEPLOYMENT_CAPABILITY_KEYS[number]

export interface DeploymentCapability {
  supported: boolean
  reason?: string
}

export type DeploymentCapabilityMap = Partial<Record<DeploymentCapabilityKey, DeploymentCapability>>

/**
 * 能力接口失败或旧版后端没有返回某个键时保持可见，避免一次探测失败把整个菜单清空。
 * 只有后端明确返回 supported: false 时才隐藏入口。
 */
export function isDeploymentCapabilitySupported(
  capabilities: DeploymentCapabilityMap,
  key?: DeploymentCapabilityKey,
  options?: { liteMode?: boolean; edition?: string },
): boolean {
  if (!key) return true
  if (key === 'organizations') {
    const isLite =
      options?.liteMode === true ||
      options?.edition?.trim().toLowerCase() === 'lite'
    if (isLite) return false
  }
  return capabilities[key]?.supported !== false
}

export const SETTINGS_SECTION_CAPABILITY: Partial<Record<string, DeploymentCapabilityKey>> = {
  websearch: 'settings.websearch',
  vectorstore: 'settings.vectorstore',
  storage: 'settings.storage',
  sandbox: 'settings.sandbox',
  // Skills are baked into a sandbox image. Hide the catalog when the
  // deployment has no sandbox support, same as personal skill credentials.
  skills: 'settings.sandbox',
  // Skill credentials exist only because sandboxes do: the values are injected
  // into a skill script's process. A deployment without sandbox support has
  // nowhere to inject them, so the page would only ever show its empty state.
  envvars: 'settings.sandbox',
  mcp: 'settings.mcp',
}
