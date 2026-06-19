import {
  createChatMarkdownRenderer,
  renderChatMarkdown,
} from './chatMarkdownRenderer.ts'
import { joinCitationTagsToPreviousLine } from './citationMarkdown.ts'

const renderer = createChatMarkdownRenderer()
const snippet = [
  '该数据集...例如：',
  '```',
  'APR = ((Fees + Interest) / Principal) × (365 / Days in Loan Term)',
  '```',
  '<kb doc="2502.08127v1.pdf" chunk_id="1ecdce8a-f922-4d0c-b124-257ab4634da2" />',
  '',
  '### 重要性',
  '',
  '1. **AAAAA**',
].join('\n')

console.log('--- joinCitationTagsToPreviousLine output ---')
console.log(JSON.stringify(joinCitationTagsToPreviousLine(snippet)))

console.log('\n--- final render ---')
console.log(renderChatMarkdown(snippet, {
  renderer,
  escapeMarkdown: (t) => t,
  sanitizeHtml: (h) => h,
  streaming: false,
  knowledgeReferences: [{ doc: '2502.08127v1.pdf', chunk_id: '1ecdce8a-f922-4d0c-b124-257ab4634da2' }],
}))
