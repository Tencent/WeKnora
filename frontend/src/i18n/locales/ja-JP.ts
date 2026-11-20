import common from './ja-JP/common.ts'
import auth from './ja-JP/auth.ts'
import chat from './ja-JP/chat.ts'
import agent from './ja-JP/agent.ts'
import knowledge from './ja-JP/knowledge.ts'
import datasource from './ja-JP/datasource.ts'
import model from './ja-JP/model.ts'
import settings from './ja-JP/settings.ts'
import system from './ja-JP/system.ts'

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
