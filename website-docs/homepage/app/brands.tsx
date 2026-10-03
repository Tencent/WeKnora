import Image from "next/image";
import { Icon } from "./ui";
import { homeAssets } from "../../shared/header";

type Integration = { name: string; logo: string } | { name: string; icon: string };

export const dataSources: Integration[] = [
  { name: "PDF / Office", icon: "file" },
  { name: "飞书", logo: "feishu.ico" },
  { name: "Confluence", logo: "confluence.svg" },
  { name: "钉钉文档", logo: "dingtalk.svg" },
  { name: "GitLab", logo: "gitlab.png" },
  { name: "腾讯 IMA", logo: "ima.png" },
  { name: "Notion", logo: "notion.ico" },
  { name: "语雀", logo: "yuque.ico" },
  { name: "RSS", logo: "rss.svg" },
];

export const clients: Integration[] = [
  { name: "企业微信", logo: "wecom.svg" },
  { name: "飞书", logo: "feishu.ico" },
  { name: "钉钉", logo: "dingtalk.svg" },
  { name: "Slack", logo: "slack.svg" },
  { name: "Telegram", logo: "telegram.svg" },
  { name: "Chrome", logo: "chrome.svg" },
  { name: "BrowserSkill", logo: "browserskill.png" },
  { name: "MCP Server", logo: "mcp.svg" },
  { name: "API", icon: "code" },
  { name: "CLI", icon: "terminal" },
  { name: "DeepSeek Harness", logo: "deepseek-color.svg" },
];

export const modelProviders: Integration[] = [
  { name: "OpenAI", logo: "openai.svg" },
  { name: "Claude", logo: "anthropic.svg" },
  { name: "Gemini", logo: "gemini-color.svg" },
  { name: "DeepSeek", logo: "deepseek-color.svg" },
  { name: "Qwen", logo: "qwen-color.svg" },
  { name: "混元", logo: "hunyuan-color.svg" },
  { name: "智谱", logo: "zhipu.svg" },
  { name: "Kimi", logo: "moonshot.svg" },
  { name: "火山引擎", logo: "volcengine.svg" },
  { name: "MiniMax", logo: "minimax.svg" },
  { name: "Ollama", logo: "ollama.svg" },
  { name: "LiteLLM", logo: "litellm.ico" },
];

export function IntegrationMark({ item }: { item: Integration }) {
  return <>
    {"logo" in item
      ? <Image src={`${homeAssets}/brands/${item.logo}`} alt="" aria-hidden="true" width={22} height={22} />
      : <Icon name={item.icon} />}
    {item.name}
  </>;
}
