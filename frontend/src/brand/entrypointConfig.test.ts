import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { DEFAULT_BRAND } from './defaults.ts'

/**
 * M13：docker-entrypoint.sh 生成 config.js 的校验与安全拼接。
 *
 * 运行时镜像是 nginx:alpine，没有 node/jq，所以 entrypoint 不做完整 JSON 解析；
 * 它只需要保证「生成的 config.js 永远是合法 JS」，字段级校验交给 brand/runtime.ts。
 * 这里做源码级断言，真实容器执行证据记录在 05-tests.md 的 R1 复验章节。
 */

const source = readFileSync(new URL('../../docker-entrypoint.sh', import.meta.url), 'utf8')
const publicConfig = readFileSync(new URL('../../public/config.js', import.meta.url), 'utf8')

test('MAX_FILE_SIZE_MB / MAX_SKILL_BUNDLE_SIZE_MB are numeric-validated with fallback', () => {
  assert.match(source, /case "\$1" in\s+''\|\*\[!0-9\]\*\) return 1 ;;/)
  assert.match(source, /\[ "\$1" -ge 1 \] && \[ "\$1" -le 10240 \]/)
  assert.match(source, /FILE_MB=\$\{MAX_FILE_SIZE_MB:-\$DEFAULT_FILE_MB\}[\s\S]*?is_positive_number "\$FILE_MB"/)
  assert.match(source, /SKILL_MB=\$\{MAX_SKILL_BUNDLE_SIZE_MB:-\$DEFAULT_SKILL_MB\}[\s\S]*?is_positive_number "\$SKILL_MB"/)
  assert.match(source, /回退 \$\{DEFAULT_FILE_MB\}/)
  assert.match(source, /回退 \$\{DEFAULT_SKILL_MB\}/)
})

test('BRAND_CONFIG_JSON is base64-wrapped and parsed in try/catch, never interpolated raw', () => {
  assert.match(source, /BRAND_B64=\$\(printf '%s' "\$BRAND_JSON_TRIMMED" \| base64 \| tr -d '\\n'\)/)
  assert.match(source, /atob\("\$\{BRAND_B64\}"\)/)
  assert.match(source, /JSON\.parse\(new TextDecoder\("utf-8"\)\.decode\(bytes\)\)/)
  assert.match(source, /catch \(error\) \{[\s\S]*?回退默认品牌配置/)
  // 结构上保证 config.js 合法：只写 BRAND_EXPR，不写原始环境变量。
  const heredoc = source.slice(source.indexOf('cat > /usr/share/nginx/html/config.js'))
  assert.match(heredoc, /BRAND: \$\{BRAND_EXPR\}/)
  assert.doesNotMatch(heredoc, /\$\{BRAND_CONFIG_JSON\}/)
  // 形态检查在启动阶段给出可定位告警。
  assert.match(source, /BRAND_CONFIG_JSON 里找不到 productName/)
  assert.match(source, /BRAND_CONFIG_JSON 必须以 \{ 开头、以 \} 结尾/)
})

test('entrypoint default brand matches the frontend defaults and leaves actionColor derived', () => {
  const match = source.match(/DEFAULT_BRAND_JSON='(\{[\s\S]*?\})'/)
  assert.ok(match, 'docker-entrypoint.sh must keep DEFAULT_BRAND_JSON')
  const parsed = JSON.parse(match![1]!) as {
    productName: string
    primaryColor: string
    actionColor?: string
    productLines: Array<{ id: string; agentId: string }>
  }
  assert.equal(parsed.productName, DEFAULT_BRAND.productName)
  assert.equal(parsed.primaryColor, DEFAULT_BRAND.primaryColor)
  assert.equal(parsed.actionColor, undefined, 'actionColor must stay derived from primaryColor (H3)')
  assert.deepEqual(parsed.productLines.map((line) => line.id), DEFAULT_BRAND.productLines.map((line) => line.id))
  assert.deepEqual(
    parsed.productLines.map((line) => line.agentId),
    DEFAULT_BRAND.productLines.map((line) => line.agentId),
  )
})

test('public/config.js keeps the same default brand shape (no hardcoded actionColor)', () => {
  assert.match(publicConfig, /primaryColor: '#0EA5E9'/)
  assert.doesNotMatch(publicConfig, /actionColor:/)
  assert.match(publicConfig, /agentId: 'builtin-quick-answer'/)
})
