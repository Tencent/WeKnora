import { computed, ref, shallowRef, watch } from 'vue'

interface Options {
  enabled: () => boolean
  send: (action: 'type' | 'press', fields: Record<string, string>) => void
  onTooLong: () => void
}

// A local IME target over the remote image. Only committed text crosses the
// connection; keydown remains responsible for navigation/editing keys.
export function useBrowserTextInput(options: Options) {
  const element = shallowRef<HTMLTextAreaElement | null>(null)
  const composing = ref(false)
  const draft = ref('')
  const anchor = ref({ x: 2, y: 2 })
  let focused = false
  let lastComposition: string | null = null
  const style = computed(() => {
    const width = composing.value ? Math.max(48, Math.min(320, [...draft.value].length * 16 + 16)) : 2
    return {
      left: `clamp(0px, ${anchor.value.x}%, calc(100% - ${width}px))`,
      top: `clamp(0px, ${anchor.value.y}%, calc(100% - 24px))`,
      width: `${width}px`,
    }
  })
  function clear() {
    if (element.value) element.value.value = ''
    draft.value = ''
  }
  function reset() {
    focused = false
    composing.value = false
    lastComposition = null
    clear()
    element.value?.blur()
  }
  watch(options.enabled, enabled => { if (!enabled) reset() }, { flush: 'sync' })
  function focusAt(x?: number, y?: number) {
    if (!options.enabled() || !element.value) return
    if (x !== undefined && y !== undefined) anchor.value = { x: Math.max(0, Math.min(100, x)), y: Math.max(0, Math.min(100, y)) }
    element.value.focus({ preventScroll: true })
    focused = true
  }
  const acceptsInput = () => focused && options.enabled()
  function insert(text: string) {
    if (!text || !acceptsInput()) return
    if (text.length > 10000) { options.onTooLong(); return }
    options.send('type', { text })
  }
  function onCompositionStart() {
    if (!acceptsInput()) return
    composing.value = true
    lastComposition = null
    draft.value = ''
  }
  function onCompositionUpdate(event: CompositionEvent) {
    if (composing.value) draft.value = event.data
  }
  function onCompositionEnd(event: CompositionEvent) {
    if (!composing.value) { clear(); return }
    composing.value = false
    lastComposition = event.data
    clear()
    insert(event.data)
  }
  function onInput(event: Event) {
    const input = event as InputEvent
    if (composing.value || input.isComposing) return
    const text = element.value?.value || input.data || ''
    // Chromium/Safari can emit their final input after compositionend; Firefox
    // may emit it before. A genuine subsequent key/paste begins a new edit.
    if (lastComposition !== null && (text === lastComposition || input.inputType?.includes('Composition'))) {
      lastComposition = null; clear(); return
    }
    lastComposition = null
    clear()
    insert(text)
  }
  function onPaste(event: ClipboardEvent) {
    if (!acceptsInput() || composing.value) return
    event.preventDefault()
    lastComposition = null
    clear()
    insert(event.clipboardData?.getData('text/plain') || '')
  }
  function onBeforeInput(event: Event) {
    const input = event as InputEvent
    if (!acceptsInput() || composing.value || input.isComposing) return
    const key = ({ deleteContentBackward: 'Backspace', deleteContentForward: 'Delete', insertLineBreak: 'Enter', insertParagraph: 'Enter' } as Record<string, string>)[input.inputType]
    if (key) { input.preventDefault(); options.send('press', { key }) }
  }
  function onKey(event: KeyboardEvent) {
    if (!acceptsInput() || composing.value || event.isComposing || event.keyCode === 229) return
    lastComposition = null
    let key = event.key
    if ((event.ctrlKey || event.metaKey) && key.toLowerCase() === 'a') key = 'ControlOrMeta+A'
    else if (event.shiftKey && key === 'Tab') key = 'Shift+Tab'
    else if (event.ctrlKey || event.metaKey || event.altKey) return
    if (['Enter', 'Tab', 'Shift+Tab', 'Backspace', 'Delete', 'Escape', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End', 'ControlOrMeta+A'].includes(key)) {
      event.preventDefault()
      options.send('press', { key })
    }
    // Printable and dead keys use input, preserving the user's keyboard layout.
  }
  function onBlur() {
    focused = false; composing.value = false; lastComposition = null; clear()
  }
  return { element, composing, style, focusAt, reset, onKey, onInput, onBeforeInput, onPaste, onCompositionStart, onCompositionUpdate, onCompositionEnd, onBlur }
}
