export interface DataSourceSelectionDefaults {
  resourceIds: string[]
  settings: Record<string, unknown>
  syncSchedule: string
  syncMode: 'incremental' | 'full'
  conflictStrategy: 'overwrite' | 'skip'
  syncDeletions: boolean
}

export function getDataSourceSelectionDefaults(type: string): DataSourceSelectionDefaults {
  if (type === 'wecom_chat_archive') {
    return {
      resourceIds: ['all'],
      settings: {
        sync_scope: 'all_archived_conversations',
        aggregation: 'conversation_day',
        timezone: 'Asia/Shanghai',
        full_sync_days: 90,
        include_message_types: ['text', 'markdown', 'link', 'news', 'mixed'],
        attachment_policy: 'metadata_only',
        include_sender_name: true,
        include_sender_id: true,
        include_room_id: true,
        include_external_user_id: true,
        sync_revoke_as_delete: false,
        record_participants_for_acl: true,
      },
      syncSchedule: '0 */30 * * * *',
      syncMode: 'incremental',
      conflictStrategy: 'overwrite',
      syncDeletions: false,
    }
  }

  return {
    resourceIds: [],
    settings: {},
    syncSchedule: '0 0 */6 * * *',
    syncMode: 'incremental',
    conflictStrategy: 'overwrite',
    syncDeletions: true,
  }
}
