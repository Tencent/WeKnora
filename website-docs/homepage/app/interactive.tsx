"use client";
import { BrandLogo } from "./brand-logo";
import { useRef, useState, useSyncExternalStore } from "react";
import { Icon } from "./ui";
import { getThemeSnapshot, subscribeToTheme, toggleTheme } from "./theme";
import { siteNavigation, repositoryUrl, headerIcons, homeAssets } from "../../shared/header";
import s from "./home.module.css";
const videoUrl = "https://github.com/user-attachments/assets/2819598d-3140-4623-814a-8162a22b653c";

function HeaderIcon({ name }: { name: string }) {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d={headerIcons[name]} /></svg>;
}
function ThemeToggle() {
  const dark = useSyncExternalStore(subscribeToTheme, getThemeSnapshot, () => false);
  const label = dark ? "切换到浅色" : "切换到深色";
  return <button className="wk-theme-toggle" type="button" role="switch" aria-checked={dark} aria-label={label} title={label} onClick={toggleTheme}><HeaderIcon name={dark ? "moon" : "sun"} /></button>;
}
export function Header() {
  const [open, setOpen] = useState(false);
  const menu = useRef<HTMLButtonElement>(null);
  return <header className="wk-header" onKeyDown={event => { if (event.key === "Escape" && open) { setOpen(false); menu.current?.focus(); } }}>
    <div className="wk-header-inner">
      <a className="wk-brand" href="/" aria-label="WeKnora 首页"><BrandLogo priority /></a>
      <nav id="main-navigation" className={`wk-navigation ${open ? "is-open" : ""}`} aria-label="主导航" onClick={() => setOpen(false)}>
        {siteNavigation.map(item => <a key={item.href} href={item.href}>{item.label}{item.badge && <span className="wk-new-label">{item.badge}</span>}</a>)}
        <a className="wk-mobile-github" href={repositoryUrl} target="_blank" rel="noreferrer">GitHub <HeaderIcon name="external" /></a>
      </nav>
      <ThemeToggle />
      <a className="wk-header-github" href={repositoryUrl} target="_blank" rel="noreferrer"><HeaderIcon name="github" /><span>GitHub</span></a>
      <button ref={menu} type="button" className="wk-menu-toggle" aria-label={open ? "关闭导航" : "打开导航"} aria-expanded={open} aria-controls="main-navigation" onClick={() => setOpen(!open)}><HeaderIcon name={open ? "close" : "menu"} /></button>
    </div>
  </header>;
}
export function ProductVideo() {
  const player = useRef<HTMLVideoElement>(null);
  const [started, setStarted] = useState(false);
  const [failed, setFailed] = useState(false);
  function play() {
    setStarted(true);
    const video = player.current;
    if (video) {
      video.src = videoUrl;
      video.play().catch(() => { /* Keep native controls available when another playback gesture is required. */ });
      video.focus();
    }
  }
  return <figure id="demo" className={s.videoFigure}>
    <div className={s.videoTop}><span>产品演示 <span className={s.videoDot}>/</span> PRODUCT FILM</span><span>02:25 <span className={s.videoDot}>/</span> 1080P</span></div>
    <div className={s.videoStage}>
      <video ref={player} controls={started} playsInline preload="none" poster={`${homeAssets}/product/agent-chat.png`} aria-label="WeKnora 产品介绍，英文旁白，中英字幕" aria-describedby="video-caption" tabIndex={started ? 0 : -1} onError={() => setFailed(true)} />
      {!started && <button className={s.videoCover} onClick={play} aria-label="播放 WeKnora 产品介绍视频，2 分 25 秒"><span className={s.playCircle}><Icon name="play" /></span><span className={s.videoCoverTitle}>WeKnora 产品演示</span><span className={s.videoCoverHint}>播放产品介绍 · 2 分 25 秒</span></button>}
      {failed && <div className={s.videoError} role="status"><p>视频暂时无法加载</p><a href={videoUrl} target="_blank" rel="noreferrer">在 GitHub 打开原视频 <Icon name="external" /></a></div>}
    </div>
    <figcaption id="video-caption" className={s.videoCaption}><span>知识问答、Agent 推理与 Wiki 整理</span><a href={videoUrl} target="_blank" rel="noreferrer">README 产品视频 · 英文旁白 / 中英字幕 <Icon name="external" /></a></figcaption>
  </figure>;
}
