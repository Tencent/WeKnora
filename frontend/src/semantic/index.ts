// 数据建模模块 (semantic studio) 入口: 在路由加载本模块时把自带的
// locale 消息合并进主 i18n 实例, 不触碰 4 个上游 locale 大文件。
//
// router/index.ts 通过副作用导入 (`import '@/semantic'`) 加载本模块,
// 因此注册必须在模块顶层立即执行。
import { mergeLocaleMessage } from './locales'

mergeLocaleMessage()
