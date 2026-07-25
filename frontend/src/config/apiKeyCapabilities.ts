import type { TenantAPIKeyCapability } from '@/api/tenant'

export type ApiKeyCapabilityOption = {
  value: TenantAPIKeyCapability
  labelKey: string
  hintKey: string
}

export type ApiKeyCapabilityGroup = {
  key: string
  labelKey: string
  capabilities: ApiKeyCapabilityOption[]
}

export const TENANT_API_KEY_CAPABILITIES: TenantAPIKeyCapability[] = [
  'retrieve', 'chat', 'read_agents', 'ingest', 'manage_kbs',
  'message_history', 'manage_agents', 'manage_mcp_services',
  'manage_datasources', 'manage_models', 'manage_vector_stores',
  'manage_storage_backends', 'manage_web_search', 'manage_channels',
  'run_evaluations', 'manage_members', 'manage_spaces',
  'manage_tenant_settings',
]

export const SYSTEM_API_KEY_CAPABILITIES: TenantAPIKeyCapability[] = [
  'system_tenants_read', 'system_tenants_manage',
  'system_settings_read', 'system_settings_manage',
  'system_runtime_read', 'system_runtime_manage', 'system_audit_read',
]

export const DEFAULT_TENANT_API_KEY_CAPABILITIES = new Set<TenantAPIKeyCapability>([
  'retrieve', 'chat', 'read_agents',
])

export const KB_SCOPED_API_KEY_CAPABILITIES = new Set<TenantAPIKeyCapability>([
  'retrieve', 'chat', 'ingest', 'manage_kbs', 'manage_agents', 'manage_datasources',
])

export const TENANT_API_KEY_CAPABILITY_GROUPS: ApiKeyCapabilityGroup[] = [
  {
    key: 'knowledge',
    labelKey: 'integrations.api.apiKeyCapabilityGroupKnowledge',
    capabilities: [
      { value: 'retrieve', labelKey: 'integrations.api.capabilityRetrieve', hintKey: 'integrations.api.capabilityRetrieveHint' },
      { value: 'chat', labelKey: 'integrations.api.capabilityChat', hintKey: 'integrations.api.capabilityChatHint' },
      { value: 'ingest', labelKey: 'integrations.api.capabilityIngest', hintKey: 'integrations.api.capabilityIngestHint' },
      { value: 'manage_kbs', labelKey: 'integrations.api.capabilityManageKbs', hintKey: 'integrations.api.capabilityManageKbsHint' },
      { value: 'message_history', labelKey: 'integrations.api.capabilityMessageHistory', hintKey: 'integrations.api.capabilityMessageHistoryHint' },
    ],
  },
  {
    key: 'automation',
    labelKey: 'integrations.api.apiKeyCapabilityGroupAutomation',
    capabilities: [
      { value: 'read_agents', labelKey: 'integrations.api.capabilityReadAgents', hintKey: 'integrations.api.capabilityReadAgentsHint' },
      { value: 'manage_agents', labelKey: 'integrations.api.capabilityManageAgents', hintKey: 'integrations.api.capabilityManageAgentsHint' },
      { value: 'manage_mcp_services', labelKey: 'integrations.api.capabilityManageMcpServices', hintKey: 'integrations.api.capabilityManageMcpServicesHint' },
      { value: 'manage_datasources', labelKey: 'integrations.api.capabilityManageDatasources', hintKey: 'integrations.api.capabilityManageDatasourcesHint' },
    ],
  },
  {
    key: 'collaboration',
    labelKey: 'integrations.api.apiKeyCapabilityGroupCollaboration',
    capabilities: [
      { value: 'manage_members', labelKey: 'integrations.api.capabilityManageMembers', hintKey: 'integrations.api.capabilityManageMembersHint' },
      { value: 'manage_spaces', labelKey: 'integrations.api.capabilityManageSpaces', hintKey: 'integrations.api.capabilityManageSpacesHint' },
    ],
  },
  {
    key: 'tenant',
    labelKey: 'integrations.api.apiKeyCapabilityGroupTenant',
    capabilities: [
      { value: 'manage_models', labelKey: 'integrations.api.capabilityManageModels', hintKey: 'integrations.api.capabilityManageModelsHint' },
      { value: 'manage_vector_stores', labelKey: 'integrations.api.capabilityManageVectorStores', hintKey: 'integrations.api.capabilityManageVectorStoresHint' },
      { value: 'manage_storage_backends', labelKey: 'integrations.api.capabilityManageStorageBackends', hintKey: 'integrations.api.capabilityManageStorageBackendsHint' },
      { value: 'manage_web_search', labelKey: 'integrations.api.capabilityManageWebSearch', hintKey: 'integrations.api.capabilityManageWebSearchHint' },
      { value: 'manage_channels', labelKey: 'integrations.api.capabilityManageChannels', hintKey: 'integrations.api.capabilityManageChannelsHint' },
      { value: 'run_evaluations', labelKey: 'integrations.api.capabilityRunEvaluations', hintKey: 'integrations.api.capabilityRunEvaluationsHint' },
      { value: 'manage_tenant_settings', labelKey: 'integrations.api.capabilityManageTenantSettings', hintKey: 'integrations.api.capabilityManageTenantSettingsHint' },
    ],
  },
]

export const SYSTEM_API_KEY_CAPABILITY_GROUP: ApiKeyCapabilityGroup = {
  key: 'system',
  labelKey: 'platformApiKeys.systemCapabilityGroup',
  capabilities: [
    { value: 'system_tenants_read', labelKey: 'platformApiKeys.capabilities.tenantsRead', hintKey: 'platformApiKeys.capabilityHints.tenantsRead' },
    { value: 'system_tenants_manage', labelKey: 'platformApiKeys.capabilities.tenantsManage', hintKey: 'platformApiKeys.capabilityHints.tenantsManage' },
    { value: 'system_settings_read', labelKey: 'platformApiKeys.capabilities.settingsRead', hintKey: 'platformApiKeys.capabilityHints.settingsRead' },
    { value: 'system_settings_manage', labelKey: 'platformApiKeys.capabilities.settingsManage', hintKey: 'platformApiKeys.capabilityHints.settingsManage' },
    { value: 'system_runtime_read', labelKey: 'platformApiKeys.capabilities.runtimeRead', hintKey: 'platformApiKeys.capabilityHints.runtimeRead' },
    { value: 'system_runtime_manage', labelKey: 'platformApiKeys.capabilities.runtimeManage', hintKey: 'platformApiKeys.capabilityHints.runtimeManage' },
    { value: 'system_audit_read', labelKey: 'platformApiKeys.capabilities.auditRead', hintKey: 'platformApiKeys.capabilityHints.auditRead' },
  ],
}

export const PLATFORM_API_KEY_CAPABILITY_GROUPS: ApiKeyCapabilityGroup[] = [
  SYSTEM_API_KEY_CAPABILITY_GROUP,
  ...TENANT_API_KEY_CAPABILITY_GROUPS,
]
