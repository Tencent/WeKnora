import { h } from 'vue'
import type { Theme } from 'vitepress'
import DefaultTheme from 'vitepress/theme'
import Landing from './Landing.vue'
import MermaidZoom from './MermaidZoom.vue'
import Screenshot from './Screenshot.vue'
import './style.css'

export default {
  extends: DefaultTheme,
  Layout: () => h(DefaultTheme.Layout, null, { 'layout-bottom': () => h(MermaidZoom) }),
  enhanceApp({ app }) {
    app.component('Landing', Landing)
    app.component('Screenshot', Screenshot)
  },
} satisfies Theme
