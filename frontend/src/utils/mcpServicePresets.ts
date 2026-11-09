export type McpServicePreset = Readonly<{
  id: string
  name: string
  url: string
  category: 'music' | 'video' | 'image'
  searchTerms: readonly string[]
  labelKey: string
  usage: Readonly<{ en: string; zh: string }>
}>

// Reviewed connection presets, not installed services. Selecting one must not
// connect, persist a service, enable generation, or supply a shared credential.
export const MCP_SERVICE_PRESETS: readonly McpServicePreset[] = [
  {
    id: 'suno', name: 'Suno by Ace Data Cloud', category: 'music',
    url: 'https://suno.mcp.acedata.cloud/mcp',
    searchTerms: ['suno', 'music', 'audio', '音乐', '音樂', '歌曲'],
    labelKey: 'mcpServiceDialog.presets.suno',
    usage: {
      en: 'Create music with Suno through Ace Data Cloud. List current models first. Generation is billable and requires the user’s approval and service access. Submit once, keep the task ID, and query that same ID until complete and successful before presenting final audio URLs. Do not resubmit while waiting.',
      zh: '通过 Ace Data Cloud 使用 Suno 创作音乐。先查询当前模型；生成会计费，需用户批准并具有服务权限。只提交一次，保存任务 ID，持续查询同一个 ID，完成且成功后再返回最终音频地址。等待时不要重复生成。',
    },
  },
  {
    id: 'seedance', name: 'Seedance by Ace Data Cloud', category: 'video',
    url: 'https://seedance.mcp.acedata.cloud/mcp',
    searchTerms: ['seedance', 'video', '视频', '影片', '即梦', '即夢'],
    labelKey: 'mcpServiceDialog.presets.seedance',
    usage: {
      en: 'Create videos with Seedance through Ace Data Cloud. Inspect current models and the exact tool schema before choosing inputs. Generation is billable and requires user approval and service access. Submit once and query the same task ID to a successful terminal state before returning video URLs.',
      zh: '通过 Ace Data Cloud 使用 Seedance 生成视频。先查询当前模型及实际工具参数；生成会计费，需用户批准并具有服务权限。只提交一次，持续查询同一个任务 ID，成功终态后再返回视频地址。',
    },
  },
  {
    id: 'happyhorse', name: 'HappyHorse by Ace Data Cloud', category: 'video',
    url: 'https://happyhorse.mcp.acedata.cloud/mcp',
    searchTerms: ['happyhorse', 'happy horse', 'video', '视频', '影片'],
    labelKey: 'mcpServiceDialog.presets.happyhorse',
    usage: {
      en: 'Create videos with HappyHorse through Ace Data Cloud. Inspect current models and supported operations first. Generation is billable and requires user approval and service access. Keep the original task ID and query it to completion; a pending response is not a finished video and must not trigger a second generation.',
      zh: '通过 Ace Data Cloud 使用 HappyHorse 生成视频。先查询当前模型和支持的操作；生成会计费，需用户批准并具有服务权限。保存原任务 ID 并查询到完成；pending 不是成片，也不能因此再次生成。',
    },
  },
  {
    id: 'gpt-image', name: 'GPT Image by Ace Data Cloud', category: 'image',
    url: 'https://openai.mcp.acedata.cloud/mcp',
    searchTerms: ['gpt image', 'gpt-image', 'openai', 'image', '图片', '圖片', '图像'],
    labelKey: 'mcpServiceDialog.presets.gptImage',
    usage: {
      en: 'Create images with GPT Image through Ace Data Cloud. List current image models and inspect the tool schema. Image generation is billable and requires user approval and service access. For asynchronous requests, keep and query the same task ID to a successful terminal result; return the final image URL, not only a task ID.',
      zh: '通过 Ace Data Cloud 使用 GPT Image 生成图片。先查询当前图片模型并检查工具参数；生成会计费，需用户批准并具有服务权限。异步请求应保存并查询同一个任务 ID，成功终态后返回最终图片地址，不能只交付任务 ID。',
    },
  },
  {
    id: 'nano-banana', name: 'Nano Banana by Ace Data Cloud', category: 'image',
    url: 'https://nanobanana.mcp.acedata.cloud/mcp',
    searchTerms: ['nano banana', 'nanobanana', 'nano-banana', 'gemini', 'image', '图片', '圖片'],
    labelKey: 'mcpServiceDialog.presets.nanoBanana',
    usage: {
      en: 'Create and edit images with Nano Banana through Ace Data Cloud. Inspect the current tool schema for supported inputs. Generation is billable and requires user approval and service access. Treat pending asynchronous responses as unfinished: query the same task ID until successful, then return the final image URL. Do not resubmit while waiting.',
      zh: '通过 Ace Data Cloud 使用 Nano Banana 生成或编辑图片。先检查实际工具参数及支持的输入；生成会计费，需用户批准并具有服务权限。异步 pending 尚未完成，应持续查询同一个任务 ID，成功后返回图片地址；等待时不要重复生成。',
    },
  },
]

export function filterMcpPresets(query: string): readonly McpServicePreset[] {
  const terms = query.toLocaleLowerCase().trim().split(/\s+/).filter(Boolean)
  return MCP_SERVICE_PRESETS.filter(preset => {
    const text = [preset.name, preset.category, ...preset.searchTerms].join(' ').toLocaleLowerCase()
    return terms.every(term => text.includes(term))
  })
}

export function mcpPresetDraft(preset: McpServicePreset, locale: string) {
  return {
    name: preset.name,
    url: preset.url,
    transport_type: 'http-streamable' as const,
    usage_instructions: locale.startsWith('zh') ? preset.usage.zh : preset.usage.en,
    auth_config: { auth_type: 'api_key' as const, api_key_header: 'Authorization', api_key: '' },
  }
}

// Accept a copied key or a previously prefixed value, but never put the token
// in a URL or ordinary headers. The existing credential field encrypts it.
export function mcpPresetBearer(value: string): string {
  const key = value.trim().replace(/^Bearer\s+/i, '').trim()
  return key ? `Bearer ${key}` : ''
}

export function mcpPresetPlatformLink(preset: McpServicePreset, placement: 'key' | 'pricing'): string {
  const url = new URL(placement === 'key'
    ? 'https://platform.acedata.cloud/console/applications'
    : 'https://platform.acedata.cloud/models')
  // Accepted Ace attribution contract: platform entry is an open-source app,
  // while content identifies WeKnora, the individual service and placement.
  url.searchParams.set('utm_source', 'opensource')
  url.searchParams.set('utm_medium', 'referral')
  url.searchParams.set('utm_campaign', 'evergreen')
  url.searchParams.set('utm_content', `weknora_mcp_${preset.id.replaceAll('-', '_')}_${placement}`)
  return url.toString()
}
