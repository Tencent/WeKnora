import type { Metadata } from "next";
import { ModelLeaderboard } from "../../model-leaderboard";

export const metadata: Metadata = {
  title: "WeKnora 模型评测 · Chat / Embed / Rerank",
  description: "在相同数据以及配置参数下，横向对比 Chat、Embed 与 Rerank 的模型表现，并提供模型效果排行榜。",
};

export default function Page() {
  return <ModelLeaderboard />;
}
