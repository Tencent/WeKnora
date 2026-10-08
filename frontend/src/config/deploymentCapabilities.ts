export const DEPLOYMENT_CAPABILITY_KEYS = [
  'organizations',
  'agents',
  'integrations.im',
  'integrations.embed',
  'integrations.api',
  'integrations.mcpserver',
  'settings.mcp',
  'settings.websearch',
  'settings.vectorstore',
  'settings.storage',
  'settings.sandbox',
  'settings.sandbox.docker',
  'settings.sandbox.host',
  'settings.sandbox.remote',
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
  // Docker talks to a local Engine API (often docker.sock = host root), so
  // missing or failed capability probes must not leave the picker visible.
  // Host sandbox is Lite-desktop-only; keep the same fail-closed gate so a
  // missed probe does not show the new-session open-project UI on other deployments.
  // Remote sandbox is unavailable on Lite desktop; fail closed like docker/host.
  if (
    key === 'settings.sandbox.docker'
    || key === 'settings.sandbox.host'
    || key === 'settings.sandbox.remote'
  ) {
    return capabilities[key]?.supported === true
  }
  return capabilities[key]?.supported !== false
}

export const SETTINGS_SECTION_CAPABILITY: Partial<Record<string, DeploymentCapabilityKey>> = {
  websearch: 'settings.websearch',
  vectorstore: 'settings.vectorstore',
  storage: 'settings.storage',
  sandbox: 'settings.sandbox.remote',
  // Skills are baked into a sandbox image. Hide the catalog when the
  // deployment has no sandbox support, same as personal skill credentials.
  skills: 'settings.sandbox',
  // Skill credentials exist only because sandboxes do: the values are injected
  // into a skill script's process. A deployment without sandbox support has
  // nowhere to inject them, so the page would only ever show its empty state.
  envvars: 'settings.sandbox',
  mcp: 'settings.mcp',
}

/**
 * Skills and skill credentials need somewhere to run: a remote sandbox, or
 * Lite's host sandbox.
 */
export function skillSettingsSupported(capabilities: DeploymentCapabilityMap): boolean {
  if (capabilities['settings.sandbox']?.supported === false) return false
  return capabilities['settings.sandbox.remote']?.supported === true
    || capabilities['settings.sandbox.host']?.supported === true
}
