export interface WikiBacklinkPage {
  slug: string
  title: string
}

// Resolve a backlink using the server's batch title lookup first, then pages
// already loaded in the sidebar, and finally the historical slug fallback.
export function resolveWikiBacklinkTitle(
  slug: string,
  titles: Record<string, string>,
  pages: WikiBacklinkPage[],
): string {
  const fetchedTitle = titles[slug]?.trim()
  if (fetchedTitle) return fetchedTitle

  const loadedPage = pages.find(page => page.slug === slug)
  if (loadedPage?.title) return loadedPage.title

  const parts = slug.split('/')
  return parts.length > 1 ? parts.slice(1).join('/') : slug
}
