"use client";

import Image from "next/image";
import { useRef, useState, useSyncExternalStore } from "react";
import { Icon } from "./ui";
import s from "./home.module.css";

const slides = [
  { name: "页面浏览", icon: "wiki", image: "wiki-browser", title: "按主题整理，保留出处", description: "开启 Wiki 后，从知识库文档中提取人物、产品和概念，生成带来源引用的页面，按目录浏览。", alt: "Wiki 浏览器：按主题组织的目录、年假页面、关联条目和原始文档引用" },
  { name: "知识图谱", icon: "channels", image: "wiki-graph", title: "顺着链接，查看相关知识", description: "在知识图谱中查看页面之间的关系。点击条目，即可阅读相关内容。", alt: "Wiki 知识图谱：日常费用报销条目与相关页面的链接关系，右侧展示条目详情" },
  { name: "版本历史", icon: "history", image: "wiki-revision-history", title: "随时编辑，改动可回溯", description: "直接修订页面，也可让智能体协助维护。查看版本差异，必要时恢复到历史内容。", alt: "Wiki 版本历史：年假页面的历史版本列表、内容差异和回滚入口" },
];

const wideLayoutQuery = "(min-width: 1100px)";
function subscribeToLayout(notify: () => void) {
  const query = window.matchMedia(wideLayoutQuery);
  query.addEventListener("change", notify);
  return () => query.removeEventListener("change", notify);
}
const getWideLayout = () => window.matchMedia(wideLayoutQuery).matches;
const getServerLayout = () => false;

// Center intermediate slides; clamp the first and last to the real track edges.
function slidePosition(element: HTMLDivElement, index: number) {
  const slide = element.children[index] as HTMLElement;
  const first = element.children[0] as HTMLElement;
  const centered = slide.offsetLeft - first.offsetLeft + slide.offsetWidth / 2 - element.clientWidth / 2;
  return Math.max(0, Math.min(element.scrollWidth - element.clientWidth, centered));
}

export function WikiGallery() {
  const track = useRef<HTMLDivElement>(null);
  const [active, setActive] = useState(0);
  const wide = useSyncExternalStore(subscribeToLayout, getWideLayout, getServerLayout);

  function goTo(index: number) {
    const element = track.current;
    if (!element) return;
    const next = Math.max(0, Math.min(slides.length - 1, index));
    element.scrollTo({ left: slidePosition(element, next), behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "instant" : "smooth" });
  }

  function syncActive() {
    const element = track.current;
    if (!element) return;
    const distances = slides.map((_, index) => Math.abs(slidePosition(element, index) - element.scrollLeft));
    setActive(distances.indexOf(Math.min(...distances)));
  }

  return <div className={s.wikiGallery} role="region" aria-label="Wiki 产品图集" aria-roledescription={wide ? undefined : "轮播图"}>
    <div className={s.wikiGalleryToolbar}>
    <div className={s.wikiGalleryTabs} aria-label="选择 Wiki 截图">
      {slides.map((slide, index) => <button key={slide.image} type="button" aria-pressed={active === index} aria-controls="wiki-gallery-track" onClick={() => goTo(index)}><Icon name={slide.icon} />{slide.name}</button>)}
    </div>
    <div className={s.wikiGalleryControls}>
      <div><button type="button" aria-label="上一张 Wiki 截图" aria-controls="wiki-gallery-track" disabled={active === 0} onClick={() => goTo(active - 1)}><Icon name="arrow" /></button><span aria-live="polite" aria-atomic="true">{String(active + 1).padStart(2, "0")} / 03</span><button type="button" aria-label="下一张 Wiki 截图" aria-controls="wiki-gallery-track" disabled={active === slides.length - 1} onClick={() => goTo(active + 1)}><Icon name="arrow" /></button></div>
    </div>
    </div>
    <div id="wiki-gallery-track" className={s.wikiGalleryTrack} ref={track} tabIndex={wide ? -1 : 0} aria-label={wide ? "Wiki 的三项能力" : "Wiki 截图，可左右滑动或使用方向键切换"} onScroll={syncActive} onKeyDown={event => {
      if (wide || event.target !== event.currentTarget) return;
      if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
      event.preventDefault();
      goTo(event.key === "Home" ? 0 : event.key === "End" ? slides.length - 1 : active + (event.key === "ArrowRight" ? 1 : -1));
    }}>
      {slides.map((slide, index) => <figure key={slide.image} className={`${s.artifactFigure} ${s.wikiSlide}`} role="group" aria-roledescription={wide ? undefined : "幻灯片"} aria-label={`${index + 1} / ${slides.length}：${slide.name}`}>
        <div className={s.artifactHeading}><span><Icon name={slide.icon} />{slide.name}</span><a className={s.textLink} href={`/product/${slide.image}.png`} target="_blank" rel="noreferrer" aria-label={`查看${slide.name}原图（新窗口）`}>查看原图 <Icon name="external" /></a></div>
        <div className={s.artifactImage}><Image src={`/product/${slide.image}.png`} alt={slide.alt} width={3840} height={2112} sizes="(min-width: 1100px) 33vw, (max-width: 800px) 90vw, 720px" /></div>
        <figcaption className={s.wikiSlideCaption}><h3>{slide.title}</h3><p>{slide.description}</p></figcaption>
      </figure>)}
    </div>

  </div>;
}
