/** Public deployment of website-docs (VitePress, base `/docs/`). */
export const DOCS_BASE_URL = 'https://weknora.weixin.qq.com/docs/'

/**
 * Doc pages linked from the UI, as website-docs paths without the `.md`
 * suffix. docsUrl.test.ts checks each one (and its anchor) still exists.
 */
export const DOC_PAGES = {
  home: '',
  tenantAuth: '03-features/01-tenant-auth',
  models: '03-features/06-models',
  modelsCompat: '03-features/06-models#协议兼容覆盖-compat-json',
  knowledgeGraph: '03-features/09-knowledge-graph',
  imIntegration: '03-features/12-im-integration',
  apiOverview: '04-api/01-api-overview',
  troubleshootingMigrations: '01-getting-started/05-troubleshooting#database-migrations',
  sandboxDeployment: '06-development/04-sandbox-deployment',
} as const

export type DocPage = keyof typeof DOC_PAGES

export function docsUrl(page: DocPage): string {
  return DOCS_BASE_URL + DOC_PAGES[page]
}
