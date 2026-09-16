import { h } from 'vue'
import type { Theme } from 'vitepress'
import DefaultTheme from 'vitepress/theme-without-fonts'
import SiteHeader from './SiteHeader.vue'
import MermaidZoom from './MermaidZoom.vue'
import Screenshot from './Screenshot.vue'
import './style.css'

export default {
  extends: DefaultTheme,
  Layout: () => h(DefaultTheme.Layout, null, {
    'layout-bottom': () => h(MermaidZoom),
    'layout-top': () => h(SiteHeader),
    'sidebar-nav-before': () => h('p', { class: 'wk-docs-label' }, '使用文档'),
  }),
  enhanceApp({ app }) {
    app.component('Screenshot', Screenshot)
  },
} satisfies Theme
