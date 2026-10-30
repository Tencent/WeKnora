"use client";
import { BrandLogo } from "./brand-logo";
import { useRef, useState, useSyncExternalStore } from "react";
import { Icon } from "./ui";
import { getThemeSnapshot, subscribeToTheme, toggleTheme } from "./theme";
import { siteNavigation, repositoryUrl, headerIcons, homeAssets } from "../../shared/header";
import s from "./home.module.css";
const productFilms = [
  {
    id: "v090",
    label: "v0.9.0 发布",
    title: "v0.9.0 工作助手",
    summary: "WeKnora 助手、多智能体协作、自动化与执行权限",
    duration: "02:45",
    durationLabel: "2 分 45 秒",
    quality: "1080P · 60 FPS",
    audio: "英文配音与字幕",
    source: "/docs/videos/weknora-v0.9.0-en.mp4",
    original: "https://github.com/user-attachments/assets/de0e490a-e738-405e-bb3a-64b63edd7278",
    poster: "/docs/videos/weknora-v0.9.0-poster.png",
  },
  {
    id: "overview",
    label: "产品全览",
    title: "WeKnora 产品全览",
    summary: "知识问答、Agent 推理与 Wiki 整理",
    duration: "02:25",
    durationLabel: "2 分 25 秒",
    quality: "1080P",
    audio: "英文旁白 / 中英字幕",
    source: "https://github.com/user-attachments/assets/2819598d-3140-4623-814a-8162a22b653c",
    original: "https://github.com/user-attachments/assets/2819598d-3140-4623-814a-8162a22b653c",
    poster: `${homeAssets}/product/agent-chat.png`,
  },
];

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
      <a className="wk-brand" href="/" target="_self" aria-label="WeKnora 首页"><BrandLogo priority /></a>
      <nav id="main-navigation" className={`wk-navigation ${open ? "is-open" : ""}`} aria-label="主导航" onClick={() => setOpen(false)}>
        {siteNavigation.map(item => {
          if (item.children?.length) {
            return (
              <div key={item.href} className="wk-nav-menu">
                <a href={item.href}>{item.label}{item.badge && <span className="wk-new-label">{item.badge}</span>}</a>
                <button type="button" className="wk-nav-caret" aria-label={`${item.label}栏目`}><HeaderIcon name="chevron" /></button>
                <div className="wk-nav-dropdown">
                  {item.children.map(child => <a key={child.href} href={child.href}>{child.label}</a>)}
                </div>
              </div>
            );
          }
          return <a key={item.href} href={item.href}>{item.label}{item.badge && <span className="wk-new-label">{item.badge}</span>}</a>;
        })}
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
  const [selected, setSelected] = useState(0);
  const [started, setStarted] = useState(false);
  const [failed, setFailed] = useState(false);
  const film = productFilms[selected];
  function play() {
    setStarted(true);
    const video = player.current;
    if (video) {
      video.src = film.source;
      video.play().catch(() => { /* Keep native controls available when another playback gesture is required. */ });
      video.focus();
    }
  }
  return <figure id="demo" className={s.videoFigure}>
    <div className={s.videoTop}>
      <div className={s.videoChoices} role="group" aria-label="选择演示视频">
        {productFilms.map((entry, index) => <button key={entry.id} type="button" aria-label={entry.title} aria-pressed={selected === index} aria-controls="product-video-stage" onClick={() => {
          if (index === selected) return;
          const video = player.current;
          if (video) { video.pause(); video.removeAttribute("src"); video.load(); }
          setSelected(index);
          setStarted(false);
          setFailed(false);
        }}>{entry.label}</button>)}
      </div>
      <span className={s.videoMeta}>{film.duration}<span className={s.videoQuality}> / {film.quality}</span></span>
    </div>
    <div id="product-video-stage" className={s.videoStage}>
      <video key={film.id} ref={player} controls={started} playsInline preload="none" poster={film.poster} aria-label={`${film.title}，${film.audio}`} aria-describedby="video-caption" tabIndex={started ? 0 : -1} onError={() => setFailed(true)} />
      {!started && <button className={s.videoCover} onClick={play} aria-label={`播放 ${film.title}视频，${film.durationLabel}`}><span className={s.playCircle}><Icon name="play" /></span><span className={s.videoCoverHint}>播放 · {film.durationLabel}</span></button>}
      {failed && <div className={s.videoError} role="status"><p>视频暂时无法加载</p><a href={film.original} target="_blank" rel="noreferrer">在 GitHub 打开原视频 <Icon name="external" /></a></div>}
    </div>
    <figcaption id="video-caption" className={s.videoCaption}><span>{film.summary}</span><a href={film.original} target="_blank" rel="noreferrer">{film.audio} <Icon name="external" /></a></figcaption>
  </figure>;
}
