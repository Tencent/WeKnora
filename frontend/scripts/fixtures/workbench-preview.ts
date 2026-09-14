import { createApp, h } from 'vue'
import { createI18n } from 'vue-i18n'
import { createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import TDesign from 'tdesign-vue-next'
import 'tdesign-vue-next/es/style/index.css'
import en from '../../src/i18n/locales/en-US'
import WorkbenchFiles from '../../src/views/chat/components/workbench/WorkbenchFiles.vue'
import WorkbenchArtifacts from '../../src/views/chat/components/workbench/WorkbenchArtifacts.vue'
import SandboxWorkbench from '../../src/views/chat/components/workbench/SandboxWorkbench.vue'
import DocumentPreview from '../../src/components/document-preview.vue'
import { createWorkbenchApi } from '../../src/api/sandbox-workbench'
import { get, post, patch, del, postUpload } from '../../src/utils/request'
import { installTDesignIconOfflineGuard } from '../../src/utils/tdesign-icon-offline'

installTDesignIconOfflineGuard()
const query = new URLSearchParams(location.search)
const mode = query.get('mode')
const name = query.get('name') || ''
const sessionId = 'office-preview-fixture'
const signal = new AbortController().signal
const common = { active: true, signal, revision: 0, maxBytes: 8 * 1024 * 1024 }
const api = createWorkbenchApi(sessionId, { get, post, patch, del, postUpload }, signal)
const sourceBlob = mode === 'baseline' ? await api.download(name) : undefined
const app = createApp({
  render: () => mode === 'live' ? h(WorkbenchFiles, { ...common, api })
    : mode === 'artifact' ? h(WorkbenchArtifacts, { ...common, sessionId })
    : mode === 'status' ? h(SandboxWorkbench, { sessionId })
    : h(DocumentPreview, { sourceBlob, sourceKey: name, fileName: name, fileType: name.split('.').pop() || '', active: true }),
})
app.use(createI18n({ legacy: false, locale: 'en-US', messages: { 'en-US': en } }))
app.use(createPinia())
app.use(createRouter({ history: createMemoryHistory(), routes: [] }))
app.use(TDesign)
app.mount('#app')
