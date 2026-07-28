import { h } from 'vue'
import type { Theme } from 'vitepress'
import DefaultTheme from 'vitepress/theme'
import Landing from './Landing.vue'
import MermaidZoom from './MermaidZoom.vue'
import './style.css'

export default {
  extends: DefaultTheme,
  Layout: () => h(DefaultTheme.Layout, null, { 'layout-bottom': () => h(MermaidZoom) }),
  enhanceApp({ app }) {
    app.component('Landing', Landing)
  },
} satisfies Theme
