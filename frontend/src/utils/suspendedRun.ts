import type { InjectionKey } from 'vue'

/**
 * Provided by a chat view: re-attaches to a suspended run after its prompt was
 * answered and the server resumed it. Takes the prompt's pending id.
 */
export const resumeSuspendedRunKey: InjectionKey<(pendingId: string) => void> = Symbol('resumeSuspendedRun')
