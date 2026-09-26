// Brand logos of the builtin IM platforms, shared by the channel editor, the
// sidebar and the plugin center.
import wecomLogo from '@/assets/img/im/wecom.svg'
import feishuLogo from '@/assets/img/im/feishu.svg'
import larkLogo from '@/assets/img/im/lark.svg'
import slackLogo from '@/assets/img/im/slack.svg'
import telegramLogo from '@/assets/img/im/telegram.svg'
import dingtalkLogo from '@/assets/img/im/dingtalk.svg'
import mattermostLogo from '@/assets/img/im/mattermost.svg'
import wechatLogo from '@/assets/img/im/wechat.svg'
import qqbotLogo from '@/assets/img/im/qqbot.png'
import yunzhijiaLogo from '@/assets/img/im/yunzhijia.svg'

export const IM_PLATFORM_LOGOS: Record<string, string> = {
  wecom: wecomLogo,
  feishu: feishuLogo,
  lark: larkLogo,
  slack: slackLogo,
  telegram: telegramLogo,
  dingtalk: dingtalkLogo,
  mattermost: mattermostLogo,
  wechat: wechatLogo,
  qqbot: qqbotLogo,
  yunzhijia: yunzhijiaLogo,
}

/** A platform's logo URL, or "" when the app ships none (plugin platforms). */
export function imPlatformLogo(platform: string | undefined): string {
  return (platform && IM_PLATFORM_LOGOS[platform]) || ''
}
