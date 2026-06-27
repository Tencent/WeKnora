import feishuIcon from '@/assets/img/datasource-feishu.ico'
import notionIcon from '@/assets/img/datasource-notion.ico'
import yuqueIcon from '@/assets/img/datasource-yuque.ico'
import rssIcon from '@/assets/img/datasource-rss.svg'

export const datasourceIconMap: Record<string, string> = {
  feishu: feishuIcon,
  notion: notionIcon,
  yuque: yuqueIcon,
  rss: rssIcon,
}

export function getDatasourceIconUrl(type: string): string | undefined {
  return datasourceIconMap[type]
}
