import common from './zh-CN/common.ts'
import auth from './zh-CN/auth.ts'
import chat from './zh-CN/chat.ts'
import agent from './zh-CN/agent.ts'
import knowledge from './zh-CN/knowledge.ts'
import datasource from './zh-CN/datasource.ts'
import model from './zh-CN/model.ts'
import settings from './zh-CN/settings.ts'
import system from './zh-CN/system.ts'

export default {
  ...common,
  ...auth,
  ...chat,
  ...agent,
  ...knowledge,
  ...datasource,
  ...model,
  ...settings,
  ...system,
}
