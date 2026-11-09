export interface FolderDestination {
  path: string
  existingFolderAnchor: string
}

/**
 * A pending path is an intention to create below an existing folder, not proof
 * that the path exists. Keep the original anchor through nested creation and
 * picker reopen; a later tree refresh must never weaken it to the root.
 */
export function createFolderDestinationAnchors(initialPending: Readonly<Record<string, string>> = {}) {
  const pending = new Map(Object.entries(initialPending))
  let creatingAnchor: string | null = null
  const anchorFor = (path: string) => pending.get(path) ?? path
  return {
    begin(parentPath: string) {
      creatingAnchor = anchorFor(parentPath)
    },
    cancel() {
      creatingAnchor = null
    },
    created(path: string): FolderDestination {
      if (creatingAnchor === null) throw new Error('Choose a parent before creating a folder')
      const existingFolderAnchor = creatingAnchor
      pending.set(path, existingFolderAnchor)
      creatingAnchor = null
      return { path, existingFolderAnchor }
    },
    pick(path: string): FolderDestination {
      return { path, existingFolderAnchor: anchorFor(path) }
    },
    snapshot(): Record<string, string> {
      return Object.fromEntries(pending)
    },
  }
}
