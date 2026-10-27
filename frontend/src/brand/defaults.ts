import type { BrandConfig } from './types'

/**
 * 两条默认产线本期共用同一个智能体：WeKnora 内置的「快速问答」。
 * 同一个常量定义在 `@/api/agent` 的 `BUILTIN_QUICK_ANSWER_ID`；这里保留字面量，
 * 是因为 brand 模块只负责运行时品牌数据，不能反向依赖 api 层，否则会形成循环依赖。
 */
const SHARED_AGENT_ID = 'builtin-quick-answer'

/**
 * 内置默认品牌。
 * public/config.js 与 docker-entrypoint.sh 里的默认值要和这里保持一致；
 * 运行时配置缺字段时回落到这份数据。
 */
export const DEFAULT_BRAND: BrandConfig = {
  productName: '小爱知识库',
  logoUrl: '/brand/allstar-logo.png',
  logoWidth: 150,
  faviconUrl: '/favicon.ico',
  primaryColor: '#0EA5E9',
  // 运行时通常不配置 actionColor，由 primaryColor 派生；这个值就是默认主色的派生结果。
  // 配置里显式写了 actionColor 才覆盖（H3）。
  actionColor: '#0284C7',
  loginSubtitle: '金蝶产品知识，随问随答',
  chatDisclaimer: '回答由 AI 根据知识库生成，仅供参考',
  productLines: [
    {
      id: 'kingdee-ai-suite',
      name: '金蝶 AI 套件',
      description: 'AI 套件开通、配置、使用问题',
      agentId: SHARED_AGENT_ID,
      samples: ['如何开通智能问数？', '知识库容量上限是多少？'],
    },
    {
      id: 'kingdee-ai-starry-sky',
      name: '金蝶 AI 星空',
      description: '星空云 ERP 财务、供应链操作问题',
      agentId: SHARED_AGENT_ID,
      samples: ['凭证过账报错怎么处理？', '期末结账要检查哪些项？'],
    },
  ],
}
