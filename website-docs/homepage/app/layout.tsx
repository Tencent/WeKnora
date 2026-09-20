import type { Metadata } from "next";
import "./globals.css";
import { themeInitializationScript } from "./theme";

export const metadata: Metadata = {
  title: "WeKnora — 帮你找到答案，并将知识付诸实践",
  description: "腾讯开源知识框架 WeKnora，集 RAG 问答、Agent 推理与自动 Wiki 于一体。v0.8.0 带来技能目录、会话级沙箱和跨会话长期记忆，支持私有化部署。",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="zh-CN" className="h-full antialiased" suppressHydrationWarning>
      <head><script dangerouslySetInnerHTML={{ __html: themeInitializationScript }} /></head>
      <body className="min-h-full flex flex-col">{children}</body>
    </html>
  );
}
