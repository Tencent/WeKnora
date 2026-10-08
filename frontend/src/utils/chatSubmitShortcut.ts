/** Enter queues during a run; Cmd/Alt+Enter steers. Preserve newline and IME behavior. */
export function chatSubmitShortcut(
  event: Pick<KeyboardEvent, 'key' | 'keyCode' | 'isComposing' | 'shiftKey' | 'ctrlKey' | 'altKey' | 'metaKey'>,
  canSteer: boolean,
): 'inject' | 'after' | undefined {
  if (event.isComposing || event.keyCode === 229 || (event.key !== 'Enter' && event.keyCode !== 13)) return
  if (event.shiftKey || event.ctrlKey) return
  if (event.metaKey) return 'inject'
  if (event.altKey) return canSteer ? 'inject' : undefined
  return canSteer ? 'after' : 'inject'
}
