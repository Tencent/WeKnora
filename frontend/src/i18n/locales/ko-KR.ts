import common from './ko-KR/common.ts'
import auth from './ko-KR/auth.ts'
import chat from './ko-KR/chat.ts'
import agent from './ko-KR/agent.ts'
import knowledge from './ko-KR/knowledge.ts'
import datasource from './ko-KR/datasource.ts'
import model from './ko-KR/model.ts'
import settings from './ko-KR/settings.ts'
import system from './ko-KR/system.ts'

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
