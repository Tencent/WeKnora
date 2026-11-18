import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess'

// diffProcessOverrides returns the fields of current that differ from the
// knowledge-base baseline. The confirm dialog builds a full form snapshot;
// storing that snapshot would pin every setting, including ones the user
// never changed.
export function diffProcessOverrides(
  baseline: KnowledgeProcessOverrides,
  current: KnowledgeProcessOverrides,
): KnowledgeProcessOverrides {
  const out: KnowledgeProcessOverrides = {}
  const keys = new Set<keyof KnowledgeProcessOverrides>([
    ...(Object.keys(baseline) as (keyof KnowledgeProcessOverrides)[]),
    ...(Object.keys(current) as (keyof KnowledgeProcessOverrides)[]),
  ])
  for (const key of keys) {
    if (stable(baseline[key]) === stable(current[key])) {
      continue
    }
    if (current[key] !== undefined) {
      ;(out as Record<string, unknown>)[key] = current[key]
    }
  }
  return out
}

function stable(value: unknown): string {
  return JSON.stringify(value ?? null)
}
