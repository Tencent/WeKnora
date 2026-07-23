import feishuIcon from '@/assets/img/datasource-feishu.ico'
import larkIcon from '@/assets/img/datasource-lark.svg'
import dingtalkIcon from '@/assets/img/im/dingtalk.svg'
import notionIcon from '@/assets/img/datasource-notion.ico'
import yuqueIcon from '@/assets/img/datasource-yuque.ico'
import rssIcon from '@/assets/img/datasource-rss.svg'

export const datasourceIconMap: Record<string, string> = {
  feishu: feishuIcon,
  lark: larkIcon,
  dingtalk: dingtalkIcon,
  notion: notionIcon,
  yuque: yuqueIcon,
  rss: rssIcon,
}

export function getDatasourceIconUrl(type: string): string | undefined {
  return datasourceIconMap[type]
}
