// Setting rows keep a stable Vue key (`item.key`) while persistSetting
// replaces the object in `settings`. Reused TDesign controls can therefore
// fire @change/@blur with a stale item whose `.value` is the pre-save
// snapshot. Event handlers must resolve by key from the latest map before
// dirty-checking or reverting the control.

export function resolveCurrentSetting<T extends { key: string }>(
  settingsByKey: ReadonlyMap<string, T>,
  key: string,
): T | undefined {
  return settingsByKey.get(key)
}

export function isSettingValueDirty(current: unknown, original: unknown): boolean {
  if (Array.isArray(current) && Array.isArray(original)) {
    if (current.length !== original.length) return true
    for (let i = 0; i < current.length; i++) {
      if (current[i] !== original[i]) return true
    }
    return false
  }
  return current !== original
}
