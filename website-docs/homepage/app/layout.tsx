import type { Metadata } from "next";
import "./globals.css";
import { themeInitializationScript } from "./theme";

export const metadata: Metadata = {
  title: "WeKnora — 帮你找到答案，并将知识付诸实践",
  description: "腾讯开源知识框架 WeKnora，集 RAG 问答、Agent 推理与自动 Wiki 于一体。v0.8.3 新增画廊、多模态 Embedding、原文引用定位、附件按需读取和平台用户管理；支持私有化部署。",
  // Reuse the documentation favicon so the homepage adds nothing at the site root.
  icons: { icon: "/docs/favicon.ico" },
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
