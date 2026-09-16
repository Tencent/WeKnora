export interface EvaluationRequestToken { readonly key: string; readonly generation: number }
export interface EvaluationRequestGate {
  begin: (key: string) => EvaluationRequestToken
  isCurrent: (token: EvaluationRequestToken) => boolean
  invalidate: () => void
}
/** Only the newest request owned by a surface may publish its result. */
export function createEvaluationRequestGate(): EvaluationRequestGate {
  let generation = 0
  let currentKey = ''
  return {
    begin: key => { currentKey = key; return { key, generation: ++generation } },
    isCurrent: token => token.generation === generation && token.key === currentKey,
    invalidate: () => { currentKey = ''; generation += 1 },
  }
}
