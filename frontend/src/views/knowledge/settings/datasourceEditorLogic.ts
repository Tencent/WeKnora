export interface ConnectorFieldDef {
  key: string
  labelKey: string
  placeholder: string
  secret?: boolean
  optional?: boolean
  hintKey?: string
  multiline?: boolean
  fieldType?: 'custom_headers'
}

export interface ConnectorDef {
  type: string
  available: boolean
  docUrl: string
  permissionDocUrl: string
  permissionPageUrl: string
  requiredPermissions: string[]
  fields: ConnectorFieldDef[]
}

export const DINGTALK_CONNECTOR_DEF: ConnectorDef = {
  type: 'dingtalk',
  available: true,
  docUrl: 'https://open.dingtalk.com/document/development/get-knowledge-base-list',
  permissionDocUrl: 'https://open.dingtalk.com/document/orgapp/get-node-list',
  permissionPageUrl: 'https://open-dev.dingtalk.com/fe/app',
  requiredPermissions: [
    'Wiki.Workspace.Read',
    'Wiki.Node.Read',
    'Storage.File.Read',
  ],
  fields: [
    { key: 'app_key', labelKey: 'datasource.field.appKey', placeholder: 'dingxxxxxxxx' },
    { key: 'app_secret', labelKey: 'datasource.field.appSecret', placeholder: '', secret: true },
    {
      key: 'operator_id',
      labelKey: 'datasource.field.operatorId',
      placeholder: '',
      hintKey: 'datasource.field.operatorIdHint',
    },
  ],
}

export function missingRequiredCredentialField(
  definition: ConnectorDef | undefined,
  credentials: Record<string, unknown>,
): ConnectorFieldDef | undefined {
  return definition?.fields.find((field) => {
    if (field.optional || field.fieldType === 'custom_headers') return false
    const value = credentials[field.key]
    return typeof value === 'string' ? value.trim() === '' : value == null
  })
}

export type ConnectionValidationMode = 'stored' | 'stateless'

export function connectionValidationMode(
  isEdit: boolean,
  credentialsConfigured: boolean,
  replacingCredentials: boolean,
): ConnectionValidationMode {
  return isEdit && credentialsConfigured && !replacingCredentials
    ? 'stored'
    : 'stateless'
}

export function usesCandidateResourcePreview(
  isEdit: boolean,
  credentialsConfigured: boolean,
  replacingCredentials: boolean,
  connectorRequiresCredentials = true,
  hasCandidateCredentials = false,
): boolean {
  return isEdit && (
    replacingCredentials ||
    (!credentialsConfigured && (connectorRequiresCredentials || hasCandidateCredentials))
  )
}

export function hasCandidateCredentialValues(
  credentials: Record<string, unknown>,
): boolean {
  return Object.values(credentials).some(
    value => typeof value === 'string' ? value.trim() !== '' : value != null,
  )
}

export function connectorRequiresCredentials(
  definition: ConnectorDef | undefined,
): boolean {
  return definition?.fields.some(
    field => !field.optional && field.fieldType !== 'custom_headers',
  ) === true
}

export function requiresResourceSelection(connectorType: string): boolean {
  return connectorType === 'dingtalk'
}

export function credentialsForMainPayload(
  isEdit: boolean,
  credentials: Record<string, unknown>,
): Record<string, unknown> {
  return isEdit ? {} : { ...credentials }
}

export function mergeLazyResources<T extends { external_id: string }>(
  existing: T[],
  children: T[],
): T[] {
  const known = new Set(existing.map((resource) => resource.external_id))
  const merged = existing.slice()
  for (const child of children) {
    if (known.has(child.external_id)) continue
    known.add(child.external_id)
    merged.push(child)
  }
  return merged
}

export interface LatestRequestGate {
  begin: () => number
  invalidate: () => number
  current: () => number
  isCurrent: (generation: number) => boolean
}

// Generation fence for UI requests that cannot always be canceled at the
// transport layer. Only the newest request may commit response state.
export function createLatestRequestGate(): LatestRequestGate {
  let generation = 0
  return {
    begin: () => ++generation,
    invalidate: () => ++generation,
    current: () => generation,
    isCurrent: candidate => candidate === generation,
  }
}
