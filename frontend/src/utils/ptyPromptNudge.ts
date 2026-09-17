/** Wait for in-flight PTY bytes before deciding the screen is empty. */
export const PTY_PROMPT_NUDGE_DELAY_MS = 120

export function xtermBufferLooksEmpty(
  getLine: (row: number) => string | undefined,
  rows: number,
): boolean {
  for (let i = 0; i < rows; i++) {
    if ((getLine(i) ?? '').trim() !== '') return false
  }
  return true
}
