const TERMINAL_STABLE_CLOSE_STATUSES = new Set([
  'needs_provision',
  'paused',
  'no_sandbox',
  'unsupported',
  'exited',
  'idle',
  'unauthorized',
])

export type TerminalReconnectDecision<T extends string> = {
  status: T | 'error'
  shouldReconnect: boolean
}

/**
 * Decide whether an ordinary transport close can safely redial. Before ready,
 * the server may already have created a Docker exec but the client does not
 * know its reconnect capability. Stable terminal states never redial.
 */
export function decideTerminalReconnect<T extends string>(input: {
  readyReceived: boolean
  reattachable: boolean
  status: T
}): TerminalReconnectDecision<T> {
  if (TERMINAL_STABLE_CLOSE_STATUSES.has(input.status)) {
    return { status: input.status, shouldReconnect: false }
  }
  return {
    status: 'error',
    shouldReconnect: input.readyReceived && input.reattachable,
  }
}

export function terminalReadyPtyId(
  reattachable: boolean,
  ptyId: number | undefined,
): number | null {
  return reattachable && typeof ptyId === 'number' && ptyId > 0 ? ptyId : null
}

export function appendTerminalPtyId(
  query: URLSearchParams,
  reattachable: boolean,
  ptyId: number | null,
): void {
  if (reattachable && ptyId && ptyId > 0) {
    query.set('pty_id', String(ptyId))
  }
}
