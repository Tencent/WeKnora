import { diffWikiLines, type WikiDiffLine } from './wikiLineDiff'

export type WikiRevisionDiffField = 'title' | 'summary' | 'content'

export interface WikiRevisionDiffSection {
  field: WikiRevisionDiffField
  lines: WikiDiffLine[]
}

export interface WikiRevisionSnapshot {
  title: string
  summary: string
  content: string
}

function hasChanges(lines: WikiDiffLine[]): boolean {
  return lines.some((line) => line.type !== 'same')
}

/** Compare a stored revision against the current page across title, summary, and body. */
export function diffWikiRevision(
  oldSnap: WikiRevisionSnapshot,
  newSnap: WikiRevisionSnapshot,
): WikiRevisionDiffSection[] {
  const sections: WikiRevisionDiffSection[] = []

  const titleLines = diffWikiLines(oldSnap.title, newSnap.title)
  if (hasChanges(titleLines)) {
    sections.push({ field: 'title', lines: titleLines })
  }

  const summaryLines = diffWikiLines(oldSnap.summary, newSnap.summary)
  if (hasChanges(summaryLines)) {
    sections.push({ field: 'summary', lines: summaryLines })
  }

  const contentLines = diffWikiLines(oldSnap.content, newSnap.content)
  if (hasChanges(contentLines)) {
    sections.push({ field: 'content', lines: contentLines })
  }

  return sections
}
