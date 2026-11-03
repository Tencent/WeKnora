export type Adopter = {
  name: string;
  industry: string;
  useCases: string[];
  description: string;
  variant: "A" | "B";
  stage: "production" | "poc" | "ecosystem";
  logo: string;
  logoSize: { width: number; height: number };
  logoScale?: number;
  link?: { href: string; label: string };
};

export const adopterIssue = "https://github.com/Tencent/WeKnora/issues/4801";

// Only brands from the supplied pack are displayed. See BRAND-ASSETS.md for sources.
export const adopters: Adopter[] = [
  {
    name: "赛乐医疗",
    industry: "医疗",
    useCases: ["员工知识助手", "智能客服"],
    description: "让企业知识服务于员工内部问答与客户咨询。",
    variant: "A",
    stage: "production",
    logo: "sifary.svg",
    logoSize: { width: 209.5, height: 58.8 },
  },
  {
    name: "出版人杂志社",
    industry: "出版传媒",
    useCases: ["专业文档分析", "知识资产沉淀"],
    description: "把出版行业的高价值信息与智库内容，装进一个可对话的媒体生态。",
    variant: "A",
    stage: "production",
    logo: "china-publishers.png",
    logoSize: { width: 1452, height: 508 },
    link: { href: "https://www.epuber.com", label: "访问官网" },
  },
  {
    name: "智达创客",
    industry: "互联网软件",
    useCases: ["知识助手", "智能客服", "文档分析"],
    description: "探索知识问答、客户服务与专业文档分析的应用。",
    variant: "B",
    // The community submission explicitly selects POC, despite "online" in the pack.
    stage: "poc",
    logo: "zhida-chuangke.png",
    logoSize: { width: 300, height: 300 },
    // The supplied image includes a wide blank margin around the complete mark.
    logoScale: 1.6,
  },
  {
    name: "腾讯云 ClawPro",
    industry: "云计算 / AI 平台",
    useCases: ["企业知识库"],
    description: "在企业 AI 智能体管控平台中集成知识库，让智能体连接企业知识。",
    variant: "B",
    stage: "ecosystem",
    logo: "clawpro.svg",
    logoSize: { width: 41, height: 40 },
    link: { href: "https://cloud.tencent.com/document/product/213/129156", label: "了解产品" },
  },
  {
    name: "光子工作室群",
    industry: "游戏",
    useCases: ["问答助手"],
    description: "将团队知识用于问答助手，支持日常资料查询。",
    variant: "A",
    stage: "production",
    logo: "lightspeed.png",
    logoSize: { width: 391, height: 115 },
    link: { href: "https://guangzi.qq.com/", label: "访问官网" },
  },
  {
    name: "腾讯客服",
    industry: "客户服务",
    useCases: ["智能客服"],
    description: "将服务知识用于智能客服，支持用户咨询与问题解答。",
    variant: "A",
    stage: "production",
    logo: "tencent-customer-service.png",
    logoSize: { width: 432, height: 64 },
    link: { href: "https://kf.qq.com/", label: "访问官网" },
  },
  {
    name: "微信游戏",
    industry: "游戏",
    useCases: ["问答助手"],
    description: "通过知识问答助手，让业务资料更便于查询和使用。",
    variant: "A",
    stage: "production",
    logo: "wechat-games.png",
    logoSize: { width: 1120, height: 486 },
    link: { href: "https://gamemp.weixin.qq.com/", label: "访问官网" },
  },
  {
    name: "腾讯云轻量应用服务器",
    industry: "云计算",
    useCases: ["WeKnora 一键部署"],
    description: "提供 WeKnora 应用模板，方便在自己的云服务器上部署和使用。",
    variant: "B",
    stage: "ecosystem",
    logo: "lighthouse.svg",
    logoSize: { width: 48, height: 48 },
    link: { href: "https://cloud.tencent.com/product/lighthouse", label: "了解产品" },
  },
];
