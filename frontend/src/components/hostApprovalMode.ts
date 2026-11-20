export const HOST_APPROVAL_MODES = ['ask', 'auto'] as const
export type HostApprovalMode = (typeof HOST_APPROVAL_MODES)[number]

export function parseHostApprovalMode(raw: unknown): HostApprovalMode {
  return raw === 'auto' ? 'auto' : 'ask'
}

export interface HostApprovalModeState {
  sessionId: string
  generation: number
  mode: HostApprovalMode
  /** True after a successful load or a user change for this session. */
  established: boolean
}

export interface HostApprovalModeLoad {
  sessionId: string
  generation: number
  /** null when the request failed. */
  mode: HostApprovalMode | null
}

// modeAfterLoad decides which mode to display after a load attempt.
// A response for another session or an older generation is ignored.
// A failed request keeps a mode the user set or that already loaded, and
// otherwise stays at ask for a session nothing has loaded yet.
export function modeAfterLoad(state: HostApprovalModeState, result: HostApprovalModeLoad): HostApprovalMode {
  if (result.sessionId !== state.sessionId || result.generation !== state.generation) {
    return state.mode
  }
  if (result.mode == null) {
    return state.established ? state.mode : 'ask'
  }
  return result.mode
}

export function readModeResponse(res: any): { mode: HostApprovalMode; defaultMode: HostApprovalMode } {
  return {
    mode: parseHostApprovalMode(res?.data?.mode),
    defaultMode: parseHostApprovalMode(res?.data?.default),
  }
}
