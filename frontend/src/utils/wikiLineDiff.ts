// Line-level diff for the wiki revision viewer. Deliberately small: the
// consumer renders "removed / added / unchanged" rows, nothing fancier, so a
// trimmed-LCS diff is enough — no need to pull in a diff dependency.

export interface WikiDiffLine {
  type: 'same' | 'add' | 'del'
  text: string
}

// Beyond this many lines (per side, after common prefix/suffix trimming) the
// O(n·m) LCS table gets expensive; fall back to a coarse "whole middle
// changed" diff, which is still honest, just less minimal.
const LCS_LINE_LIMIT = 1500

// diffWikiLines compares two texts line-by-line and returns a flat list of
// rows in display order: `del` rows come from `oldText`, `add` rows from
// `newText`, `same` rows from both. Common leading/trailing lines are
// detected first so typical wiki edits (one section changed) stay cheap even
// on long pages.
export function diffWikiLines(oldText: string, newText: string): WikiDiffLine[] {
  const oldLines = oldText.split('\n')
  const newLines = newText.split('\n')

  // Trim common prefix.
  let start = 0
  while (start < oldLines.length && start < newLines.length && oldLines[start] === newLines[start]) {
    start++
  }
  // Trim common suffix (not overlapping the prefix).
  let oldEnd = oldLines.length
  let newEnd = newLines.length
  while (oldEnd > start && newEnd > start && oldLines[oldEnd - 1] === newLines[newEnd - 1]) {
    oldEnd--
    newEnd--
  }

  const prefix: WikiDiffLine[] = oldLines.slice(0, start).map((text) => ({ type: 'same' as const, text }))
  const suffix: WikiDiffLine[] = oldLines.slice(oldEnd).map((text) => ({ type: 'same' as const, text }))
  const oldMid = oldLines.slice(start, oldEnd)
  const newMid = newLines.slice(start, newEnd)

  let middle: WikiDiffLine[]
  if (oldMid.length === 0) {
    middle = newMid.map((text) => ({ type: 'add' as const, text }))
  } else if (newMid.length === 0) {
    middle = oldMid.map((text) => ({ type: 'del' as const, text }))
  } else if (oldMid.length > LCS_LINE_LIMIT || newMid.length > LCS_LINE_LIMIT) {
    middle = [
      ...oldMid.map((text) => ({ type: 'del' as const, text })),
      ...newMid.map((text) => ({ type: 'add' as const, text })),
    ]
  } else {
    middle = lcsDiff(oldMid, newMid)
  }

  return [...prefix, ...middle, ...suffix]
}

// lcsDiff produces a minimal line diff of two smallish line arrays via the
// classic LCS dynamic program, walked back from the bottom-right corner.
function lcsDiff(a: string[], b: string[]): WikiDiffLine[] {
  const n = a.length
  const m = b.length
  // dp[i][j] = LCS length of a[i:], b[j:]
  const dp: Uint32Array[] = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1))
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1])
    }
  }
  const out: WikiDiffLine[] = []
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      out.push({ type: 'same', text: a[i] })
      i++
      j++
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      out.push({ type: 'del', text: a[i] })
      i++
    } else {
      out.push({ type: 'add', text: b[j] })
      j++
    }
  }
  while (i < n) out.push({ type: 'del', text: a[i++] })
  while (j < m) out.push({ type: 'add', text: b[j++] })
  return out
}
