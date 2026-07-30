import { createServer } from 'node:http'

const port = Number(process.env.DINGTALK_MOCK_PORT || 18080)
const state = {
  previewValidations: 0,
  previews: 0,
  creates: 0,
  resourceLists: 0,
  reconfigures: 0,
  syncs: 0,
  sawExpectedCredentials: false,
  sawExpectedCreate: false,
  sawExpectedReconfigure: false,
  unknownAPIRequests: 0,
}

function json(response, status, payload) {
  response.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Access-Control-Allow-Origin': '*',
  })
  response.end(JSON.stringify(payload))
}

async function readJSON(request) {
  const chunks = []
  for await (const chunk of request) chunks.push(chunk)
  if (chunks.length === 0) return {}
  return JSON.parse(Buffer.concat(chunks).toString('utf8'))
}

function resourcesForParent(parentID) {
  if (!parentID) {
    return [{
      external_id: 'mock-space',
      name: '钉钉产品协作空间',
      type: 'space',
      description: '',
      url: 'https://alidocs.dingtalk.com/i/mock-space',
      has_children: true,
    }]
  }
  if (parentID === 'mock-space') {
    return [{
      external_id: 'mock-folder',
      parent_id: 'mock-space',
      name: '使用指南',
      type: 'folder',
      description: '',
      url: '',
      has_children: true,
    }]
  }
  if (parentID === 'mock-folder') {
    return [{
      external_id: 'mock-document',
      parent_id: 'mock-folder',
      name: '钉钉产品使用手册',
      type: 'document',
      description: '',
      url: 'https://alidocs.dingtalk.com/i/mock-document',
      has_children: false,
    }]
  }
  return []
}

const server = createServer(async (request, response) => {
  const requestURL = new URL(request.url || '/', `http://${request.headers.host}`)
  const path = requestURL.pathname

  if (request.method === 'OPTIONS') {
    response.writeHead(204, {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Headers': 'Content-Type, Authorization',
      'Access-Control-Allow-Methods': 'GET, POST, PUT, DELETE, OPTIONS',
    })
    response.end()
    return
  }

  if (path === '/__mock/state') {
    json(response, 200, state)
    return
  }

  if (request.method === 'POST' && path === '/__mock/reset') {
    for (const key of Object.keys(state)) {
      state[key] = typeof state[key] === 'boolean' ? false : 0
    }
    json(response, 200, state)
    return
  }

  if (request.method === 'POST' && path === '/api/v1/datasource/preview-resources') {
    const body = await readJSON(request)
    const credentials = body?.credentials || {}
    const validCredentials =
      body?.type === 'dingtalk' &&
      credentials.app_key === 'mock-app-key' &&
      credentials.app_secret === 'mock-app-secret' &&
      credentials.operator_id === 'mock-operator'
    state.previews += 1
    if (body?.validate_only === true) {
      state.previewValidations += 1
      state.sawExpectedCredentials = validCredentials
    }
    if (!validCredentials) {
      json(response, 400, { success: false, error: { message: '模拟凭据与预期值不一致' } })
      return
    }
    if (body?.validate_only === true) {
      json(response, 200, { success: true, data: [] })
      return
    }
    state.resourceLists += 1
    json(response, 200, { success: true, data: resourcesForParent(body?.parent_id) })
    return
  }

  if (request.method === 'POST' && path === '/api/v1/datasource') {
    const body = await readJSON(request)
    const credentials = body?.config?.credentials || {}
    state.creates += 1
    state.sawExpectedCreate =
      body?.type === 'dingtalk' &&
      body?.status === 'active' &&
      Array.isArray(body?.config?.resource_ids) &&
      body.config.resource_ids.length === 1 &&
      body.config.resource_ids[0] === 'mock-document' &&
      credentials.app_key === 'mock-app-key' &&
      credentials.app_secret === 'mock-app-secret' &&
      credentials.operator_id === 'mock-operator'
    if (!state.sawExpectedCreate) {
      json(response, 400, { success: false, error: { message: '模拟创建请求与预期值不一致' } })
      return
    }
    json(response, 200, { success: true, data: { id: 'mock-ds' } })
    return
  }

  if (request.method === 'PUT' && path === '/api/v1/datasource/mock-ds/reconfigure') {
    const body = await readJSON(request)
    const credentials = body?.credentials || {}
    const dataSource = body?.data_source || {}
    state.reconfigures += 1
    state.sawExpectedReconfigure =
      dataSource.type === 'dingtalk' &&
      dataSource.status === 'active' &&
      Array.isArray(dataSource?.config?.resource_ids) &&
      dataSource.config.resource_ids.length === 1 &&
      dataSource.config.resource_ids[0] === 'mock-document' &&
      credentials.app_key === 'mock-app-key' &&
      credentials.app_secret === 'mock-app-secret' &&
      credentials.operator_id === 'mock-operator'
    if (!state.sawExpectedReconfigure) {
      json(response, 400, { success: false, error: { message: '模拟重新配置请求与预期值不一致' } })
      return
    }
    json(response, 200, { success: true, data: { id: 'mock-ds' } })
    return
  }

  if (request.method === 'POST' && path === '/api/v1/datasource/mock-ds/sync') {
    state.syncs += 1
    json(response, 200, {
      success: true,
      data: { id: 'mock-sync-log', status: 'running' },
    })
    return
  }

  if (path.startsWith('/api/')) {
    state.unknownAPIRequests += 1
    json(response, 404, { success: false, error: { message: `模拟服务未处理此接口：${request.method} ${path}` } })
    return
  }

  json(response, 404, { success: false, error: { message: '未找到请求的资源' } })
})

server.listen(port, '127.0.0.1', () => {
  console.log(`钉钉数据源模拟接口已启动：http://127.0.0.1:${port}`)
})
