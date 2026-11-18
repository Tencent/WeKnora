import common from './en-US/common.ts'
import auth from './en-US/auth.ts'
import chat from './en-US/chat.ts'
import agent from './en-US/agent.ts'
import knowledge from './en-US/knowledge.ts'
import datasource from './en-US/datasource.ts'
import model from './en-US/model.ts'
import settings from './en-US/settings.ts'
import system from './en-US/system.ts'

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
