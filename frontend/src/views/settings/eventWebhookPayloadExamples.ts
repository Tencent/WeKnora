export const WEBHOOK_PAYLOAD_EXAMPLE_TYPES = [
  'knowledge.created',
  'knowledge.parse_completed',
  'knowledge.parse_failed',
  'knowledge.deleted',
  'knowledge.batch_deleted',
  'kb.created',
  'kb.deleted',
  'rbac.member_added',
  'rbac.member_removed',
  'webhook.test',
] as const

export type WebhookPayloadExampleType = (typeof WEBHOOK_PAYLOAD_EXAMPLE_TYPES)[number]

export const WEBHOOK_PAYLOAD_HINT_KEYS: Record<WebhookPayloadExampleType, string> = {
  'knowledge.created': 'settings.eventWebhooks.payloadHintCreated',
  'knowledge.parse_completed': 'settings.eventWebhooks.payloadHintParsed',
  'knowledge.parse_failed': 'settings.eventWebhooks.payloadHintParseFailed',
  'knowledge.deleted': 'settings.eventWebhooks.payloadHintDeleted',
  'knowledge.batch_deleted': 'settings.eventWebhooks.payloadHintBatchDeleted',
  'kb.created': 'settings.eventWebhooks.payloadHintKbCreated',
  'kb.deleted': 'settings.eventWebhooks.payloadHintKbDeleted',
  'rbac.member_added': 'settings.eventWebhooks.payloadHintMemberAdded',
  'rbac.member_removed': 'settings.eventWebhooks.payloadHintMemberRemoved',
  'webhook.test': 'settings.eventWebhooks.payloadHintTest',
}

const KNOWLEDGE_ID = '4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5'
const KB_ID = 'b7e1a2c0-1111-2222-3333-444444444444'

const downloadFile = {
  available: true,
  reason: '',
  http_method: 'GET',
  path: `/api/v1/files/knowledge-download/${KNOWLEDGE_ID}`,
  ticket: 'wdt1.eyJwdXJwb3NlIjoia25vd2xlZGdlX2Rvd25sb2FkIiwi...redacted.sig',
  ticket_expires_at: '2026-08-31T06:06:00Z',
  ticket_header: 'X-WeKnora-Download-Ticket',
}

const downloadGone = (reason: 'deleted' | 'not_a_file') => ({
  available: false,
  reason,
  http_method: 'GET',
  path: '',
  ticket: '',
  ticket_expires_at: '',
  ticket_header: 'X-WeKnora-Download-Ticket',
})

function envelope(
  type: WebhookPayloadExampleType,
  time: string,
  data: Record<string, unknown>,
  actor = 'usr_8f2a',
  request = 'req_ab12',
) {
  return {
    spec_version: '1',
    id: 'evt_01K3N8G2R8X4Y6Z8A0B1C2D3E4',
    type,
    time,
    tenant_id: 42,
    actor_user_id: actor,
    request_id: request,
    data,
  }
}

function knowledgeData(overrides: Record<string, unknown>) {
  return {
    resource: 'knowledge',
    knowledge_id: KNOWLEDGE_ID,
    knowledge_base_id: KB_ID,
    owner_tenant_id: 42,
    title: 'example.pdf',
    knowledge_type: 'file',
    source: '',
    file_type: 'pdf',
    parse_status: 'pending',
    enable_status: 'disabled',
    folder_path: 'guides',
    error_message: '',
    deleted: false,
    knowledge_ids: [] as string[],
    count: 0,
    total_count: 0,
    batch_index: 0,
    batch_total: 0,
    delete_batch_id: '',
    truncated: false,
    download: downloadFile,
    ...overrides,
  }
}

const EXAMPLES: Record<WebhookPayloadExampleType, unknown> = {
  'knowledge.created': envelope(
    'knowledge.created',
    '2026-08-31T06:01:00Z',
    knowledgeData({}),
  ),
  'knowledge.parse_completed': envelope(
    'knowledge.parse_completed',
    '2026-08-31T06:05:00Z',
    knowledgeData({
      parse_status: 'completed',
      enable_status: 'enabled',
    }),
    '',
    '',
  ),
  'knowledge.parse_failed': envelope(
    'knowledge.parse_failed',
    '2026-08-31T06:06:00Z',
    knowledgeData({
      parse_status: 'failed',
      error_message: 'parser timeout',
    }),
    '',
    '',
  ),
  'knowledge.deleted': envelope(
    'knowledge.deleted',
    '2026-08-31T06:10:00Z',
    knowledgeData({
      parse_status: 'deleting',
      deleted: true,
      download: downloadGone('deleted'),
    }),
    'usr_8f2a',
    'req_ef56',
  ),
  'knowledge.batch_deleted': envelope(
    'knowledge.batch_deleted',
    '2026-08-31T06:11:00Z',
    knowledgeData({
      knowledge_id: '',
      title: '',
      knowledge_type: '',
      file_type: '',
      parse_status: '',
      enable_status: '',
      folder_path: '',
      deleted: true,
      knowledge_ids: [
        '11111111-1111-1111-1111-111111111111',
        '22222222-2222-2222-2222-222222222222',
      ],
      count: 2,
      total_count: 2,
      batch_index: 1,
      batch_total: 1,
      delete_batch_id: 'bdel_01K3N8M0SAMEBATCH0000000001',
      download: downloadGone('deleted'),
    }),
    'usr_8f2a',
    'req_gh78',
  ),
  'kb.created': envelope(
    'kb.created',
    '2026-08-31T06:00:00Z',
    {
      resource: 'knowledge_base',
      knowledge_base_id: KB_ID,
      name: 'Sample KB',
      visibility: 'private',
      cascade_knowledge: false,
      knowledge_count: 0,
      unavailable_to_tenant: false,
      share_id: '',
      source_tenant_id: 0,
    },
    'usr_8f2a',
    'req_ij90',
  ),
  'kb.deleted': envelope(
    'kb.deleted',
    '2026-08-31T07:00:00Z',
    {
      resource: 'knowledge_base',
      knowledge_base_id: KB_ID,
      name: 'Sample KB',
      visibility: 'private',
      cascade_knowledge: true,
      knowledge_count: 128,
      unavailable_to_tenant: true,
      share_id: '',
      source_tenant_id: 0,
    },
    'usr_8f2a',
    'req_kl12',
  ),
  'rbac.member_added': envelope(
    'rbac.member_added',
    '2026-08-31T08:00:00Z',
    {
      resource: 'member',
      user_id: 'usr_guest',
      role: 'viewer',
      reason: 'added',
      email: 'guest@example.com',
    },
    'usr_owner',
    'req_op56',
  ),
  'rbac.member_removed': envelope(
    'rbac.member_removed',
    '2026-08-31T08:01:00Z',
    {
      resource: 'member',
      user_id: 'usr_guest',
      role: 'viewer',
      reason: 'removed',
      email: 'guest@example.com',
    },
    'usr_owner',
    'req_op57',
  ),
  'webhook.test': envelope(
    'webhook.test',
    '2026-08-31T08:30:00Z',
    {
      resource: 'webhook',
      ok: true,
    },
    'usr_owner',
    '',
  ),
}

export function webhookPayloadExample(type: WebhookPayloadExampleType): string {
  return `${JSON.stringify(EXAMPLES[type], null, 2)}\n`
}
