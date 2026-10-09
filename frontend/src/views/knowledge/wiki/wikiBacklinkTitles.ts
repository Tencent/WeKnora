export interface WikiBacklinkPage {
  slug: string
  title: string
}

// Resolve a backlink using the page detail's title map first, then pages
// already loaded in the sidebar, and finally the historical slug fallback.
export function resolveWikiBacklinkTitle(
  slug: string,
  titles: Record<string, string>,
  pages: WikiBacklinkPage[],
): string {
  if (Object.prototype.hasOwnProperty.call(titles, slug)) {
    const value = titles[slug]
    if (typeof value === 'string' && value.trim()) return value.trim()
  }

  const loadedPage = pages.find(page => page.slug === slug)
  if (loadedPage?.title) return loadedPage.title

  const parts = slug.split('/')
  return parts.length > 1 ? parts.slice(1).join('/') : slug
}
