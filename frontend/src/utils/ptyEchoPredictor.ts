/**
 * Optimistic local echo for a high-latency PTY.
 *
 * xterm does not echo keystrokes; it waits for the remote shell. E2B's
 * SendInput is a unary RPC, so that wait is one data-plane RTT per key.
 * Printable input is drawn immediately and later stripped from the PTY
 * stream when the real echo arrives, so characters do not double.
 *
 * Password prompts must not be locally echoed: sudo / read -s / login
 * typically send no echo, and predicted characters would stay on screen.
 */

export type PtyEchoWrite = (chunk: string | Uint8Array) => void

export function looksLikeSecretPrompt(text: string): boolean {
  const line = (text.split(/\r\n|\n|\r/).pop() ?? '').trimEnd()
  if (/(?:password|passphrase|密码)/i.test(line)) {
    return true
  }
  return /\bpin\s*:?\s*$/i.test(line)
}

function cellWidth(ch: string): number {
  const cp = ch.codePointAt(0) ?? 0
  if (cp === 0) return 0
  if (cp <= 0x7f) return 1
  return 2
}

function displayCells(s: string): number {
  let n = 0
  for (const ch of s) n += cellWidth(ch)
  return n
}

function popChar(s: string): { rest: string; ch: string } {
  const chars = [...s]
  const ch = chars.pop() ?? ''
  return { rest: chars.join(''), ch }
}

export function createPtyEchoPredictor(write: PtyEchoWrite) {
  let predicted = ''
  let secretPrompt = false
  let recentRemote = ''
  const decoder = new TextDecoder()

  function eraseCells(n: number) {
    if (n <= 0) return
    write('\b'.repeat(n) + ' '.repeat(n) + '\b'.repeat(n))
  }

  function rewindAll() {
    eraseCells(displayCells(predicted))
    predicted = ''
  }

  function noteRemote(text: string) {
    recentRemote = (recentRemote + text).slice(-400)
    if (looksLikeSecretPrompt(recentRemote)) {
      secretPrompt = true
      rewindAll()
      return
    }
    if (!secretPrompt) return
    const line = recentRemote.split(/\r\n|\n|\r/).pop() ?? ''
    if (line.trim() !== '') {
      secretPrompt = false
    }
  }

  return {
    onLocal(data: string) {
      if (secretPrompt) {
        return
      }
      if (data === '\x7f' || data === '\b') {
        if (!predicted) return
        const { rest, ch } = popChar(predicted)
        predicted = rest
        eraseCells(cellWidth(ch))
        return
      }
      // Enter must keep the predicted line so the shell's echo of those
      // characters is stripped instead of flashing (draw, wipe, redraw).
      if (data === '\r' || data === '\n') {
        return
      }
      if (data === '\t' || data.startsWith('\x1b')) {
        rewindAll()
        return
      }
      if (data.length === 0) return
      for (const ch of data) {
        if (ch < ' ') {
          rewindAll()
          return
        }
      }
      predicted += data
      write(data)
    },

    onRemote(bytes: Uint8Array) {
      if (bytes.length === 0) return
      const text = decoder.decode(bytes, { stream: true })
      if (!text) return
      noteRemote(text)

      const predChars = [...predicted]
      const textChars = [...text]
      let i = 0
      while (i < predChars.length && i < textChars.length && predChars[i] === textChars[i]) {
        i++
      }
      predicted = predChars.slice(i).join('')
      const rest = textChars.slice(i).join('')
      if (!rest) return
      if (predicted) rewindAll()
      write(rest)
    },
  }
}
