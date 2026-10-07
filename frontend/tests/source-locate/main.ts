import { createApp } from 'vue'
import { createI18n } from 'vue-i18n'
import { Button, Loading } from 'tdesign-vue-next'
import { Icon } from 'tdesign-icons-vue-next'
import Harness from './Harness.vue'
createApp(Harness).component('t-button', Button).component('t-loading', Loading).component('t-icon', Icon).use(createI18n({ legacy: false, locale: 'en', messages: { en: {
  preview: { zoomOut: 'Zoom out', zoomIn: 'Zoom in', loadFailed: 'Load failed' },
  chat: { referenceSourcePrevious: 'Previous location', referenceSourceNext: 'Next location' },
} } })).mount('#app')
