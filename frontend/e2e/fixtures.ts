import { expect, type Page } from '@playwright/test'

const user = { id: 'mobile-test-user', username: 'Mobile Tester', email: 'mobile@example.test', tenant_id: 1, is_active: true, is_system_admin: true }
const tenant = { id: 1, name: 'Test workspace', owner_id: user.id, status: 'active' }
const kb = { id: 'mobile-kb', name: '庄子知识库：逍遥游、齐物论与历代注疏研究', type: 'document', tenant_id: 1,
  description: 'Mobile layout fixture', summary_model_id: 'chat-model', storage_backend_id: 'test-storage', is_pinned: false, knowledge_count: 1, embedding_model_id: 'embedding',
  indexing_strategy: { wiki_enabled: true }, chunking_config: { chunk_size: 512, chunk_overlap: 50 }, wiki_config: { enabled: true },
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }

export async function mockApp(page: Page) {
  page.on('pageerror', error => console.error('BROWSER', error.message))
  page.on('console', message => { if (message.type() === 'error') console.error('CONSOLE', message.text().slice(0, 400)) })
  await page.addInitScript(({ user, tenant }) => {
    localStorage.setItem('weknora_token', 'local-test-token')
    localStorage.setItem('weknora_user', JSON.stringify(user))
    localStorage.setItem('weknora_tenant', JSON.stringify(tenant))
    localStorage.setItem('locale', 'zh-CN')
    localStorage.setItem('weknora:new-user-guide-done:v1', '1')
    for (const key of ['kb-list:v2', 'kb-create:v3', 'tenant-models:v1', 'kb-detail:v1', 'chat:v1', 'agent-list:v1', 'agent-create:v1']) {
      localStorage.setItem('weknora:contextual-guide-' + key, '1')
    }
  }, { user, tenant })
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = []
    if (path.endsWith('/auth/me')) data = { user, tenant, memberships: [{ tenant_id: 1, role: 'owner' }], capabilities: { can_create_tenant: true } }
    else if (path.endsWith('/knowledge/file')) data = { id: 'uploaded-mobile-file', parse_status: 'processing' }
    else if (path.endsWith('/image-attrs/schema')) data = { version: 'attrs/2', attributes: [], default_actions: { ocr: { on: [], on_unobserved: false } } }
    else if (path.endsWith('/organizations')) data = { organizations: [], resource_counts: {} }
    else if (path.endsWith('/knowledge-bases')) data = [kb]
    else if (path.endsWith('/knowledge-bases/mobile-kb')) data = kb
    else if (path.endsWith('/models')) data = [{ id: 'chat-model', name: 'Test chat model', type: 'KnowledgeQA', source: 'remote', parameters: {} }]
    else if (path.includes('/wiki/pages/')) data = { id: 'wiki-page', slug: 'xiaoyaoyou', title: '逍遥游', page_type: 'concept', content: '# 逍遥游\n\n北冥有鱼，其名为鲲。\n\n' + '阅读内容。'.repeat(300), source_refs: [], in_links: [], out_links: [], aliases: [], page_metadata: {} }
    else if (path.endsWith('/wiki/pages')) data = { pages: [{ id: 'wiki-page', slug: 'xiaoyaoyou', title: '逍遥游', page_type: 'concept', status: 'published', source_refs: [], in_links: [], out_links: [] }], total: 1, total_pages: 1 }
    else if (path.endsWith('/wiki/stats')) data = { total_pages: 1, total_entities: 1, pending_issues: 0, pages_by_type: { concept: 1 } }
    else if (path.endsWith('/wiki/folders')) data = { folders: [] }
    else if (path.endsWith('/wiki/graph')) data = { nodes: [{ id: 'xiaoyaoyou', slug: 'xiaoyaoyou', title: '逍遥游', page_type: 'concept', link_count: 1 }], edges: [] }
    else if (path.includes('/knowledge') && path.includes('/files')) data = { data: [], total: 0 }
    else if (path.includes('/sessions')) data = []
    else if (path.includes('/config') || path.includes('/preferences') || path.includes('/settings')) data = {}
    else if (['/api/v1/system/capabilities', '/api/v1/system/info', '/api/v1/me/browser'].includes(path)) data = {}
    else if (['/api/v1/shared-knowledge-bases', '/api/v1/agents', '/api/v1/shared-agents', '/api/v1/web-search-providers', '/api/v1/im-channels', '/api/v1/embed-channels', '/api/v1/mcp-services', '/api/v1/skills', '/api/v1/builtin-prompts', '/api/v1/tenants/storage-backends'].includes(path)) data = []
    else if (/^\/api\/v1\/knowledge-bases\/[^/]+\/(knowledge|tags|folders|capabilities|faq|recommended-questions)$/.test(path)) data = []
    else if (/^\/api\/v1\/tenants\/kv\//.test(path)) data = {}
    else if (path.endsWith('/suggested-questions')) data = { questions: [] }
    else if (path === '/api/v1/me/invitations/pending-count') data = { count: 0 }
    else if (path === '/api/v1/system/parser-engines') data = []
    else if (/^\/api\/v1\/knowledge-bases\/[^/]+\/knowledge\/(folders|tags|stats)$/.test(path)) data = []
    else if (/^\/api\/v1\/knowledge-bases\/[^/]+\/faq\/(entries|tags|stats)$/.test(path)) data = []
    else if (['/api/v1/user/favorites','/api/v1/user/recents'].includes(path)) data = []
    else if (path.endsWith('/wiki/index')) data = { intro: '', version: 1, groups: [] }
    else if (path === '/api/v1/system/storage-engine-status') data = { engines: [], minio_env_available: false }
    else if (path.endsWith('/wiki/issues')) data = { issues: [], total: 0 }
    else throw new Error(`Unhandled fixture API: ${route.request().method()} ${path}`)
    await route.fulfill({ json: { success: true, data, total: Array.isArray(data) ? data.length : 0 } })
  })
}

export async function fitsViewport(page: Page) {
  const sizes = await page.evaluate(() => ({ viewport: document.documentElement.clientWidth, width: document.documentElement.scrollWidth }))
  expect(sizes.width).toBeLessThanOrEqual(sizes.viewport + 1)
}
