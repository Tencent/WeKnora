"use client";

import Image from "next/image";
import { useEffect, useRef, useState, type CSSProperties } from "react";
import { Icon } from "./ui";
import { homeAssets } from "../../shared/header";
import s from "./home.module.css";

export type GalleryShot = { label?: string; icon?: string; image: string; alt: string; available: boolean };
export type GallerySlide = {
  name: string;
  icon: string;
  title: string;
  description: string;
  /** One screenshot, or several views of the same capability switched inside the figure. */
  shots: GalleryShot[];
  link?: string;
  /** Optional external reference shown beside "了解更多", e.g. an upstream repository. */
  external?: { label: string; href: string; logo?: string };
};

// The showcase plays itself once it is on screen: every screenshot stays for
// STEP_MS, and multi-view items walk through their views before moving on.
// Keyboard focus pauses it; any click stops it for good.
const STEP_MS = 4000;
const AUTO_ROUNDS = 2;

/** A feature list beside one large screenshot; the list doubles as the navigation. */
export function ProductGallery({ id, label, slides }: { id: string; label: string; slides: GallerySlide[] }) {
  const root = useRef<HTMLDivElement>(null);
  const [item, setItem] = useState(0);
  const [shot, setShot] = useState(0);
  const [auto, setAuto] = useState(true);
  const [steps, setSteps] = useState(0);
  const [visible, setVisible] = useState(false);
  const [paused, setPaused] = useState(false);
  const totalSteps = slides.reduce((count, slide) => count + slide.shots.length, 0);
  const active = slides[item];
  const activeShot = active.shots[shot];

  useEffect(() => {
    const element = root.current;
    if (!element) return;
    const observer = new IntersectionObserver(([entry]) => setVisible(entry.isIntersecting), { threshold: 0.4 });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  const running = auto && visible && !paused;
  useEffect(() => {
    if (!running || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const timer = window.setTimeout(() => {
      const lastView = shot + 1 >= slides[item].shots.length;
      setShot(lastView ? 0 : shot + 1);
      if (lastView) setItem((item + 1) % slides.length);
      setSteps(steps + 1);
      if (steps + 1 >= totalSteps * AUTO_ROUNDS) setAuto(false);
    }, STEP_MS);
    return () => window.clearTimeout(timer);
  }, [running, item, shot, steps, slides, totalSteps]);

  function selectItem(index: number) {
    setAuto(false);
    setItem(index);
    setShot(0);
  }

  function selectView(index: number, view: number) {
    setAuto(false);
    setItem(index);
    setShot(view);
  }

  return <div id={id} ref={root} className={s.showcase} onFocus={() => setPaused(true)} onBlur={() => setPaused(false)}>
    <div className={s.showcaseList} aria-label={`${label} 功能`}>
      {slides.map((slide, index) => <div key={slide.name} className={s.showcaseItem} data-active={index === item} onClick={() => { if (index !== item) selectItem(index); }}>
        <h3><button type="button" aria-pressed={index === item} onClick={event => { event.stopPropagation(); selectItem(index); }}>
          <span className={s.showcaseName}><Icon name={slide.icon} />{slide.name}</span>
          <span className={s.showcaseTitle}>{slide.title}</span>
        </button></h3>
        <div className={s.showcaseDetail}>
          <p>{slide.description}</p>
          {slide.shots.length > 1 && <div className={s.viewChips} aria-label={`${slide.name}截图`}>
            {slide.shots.map((entry, view) => {
              const current = index === item && view === shot;
              return <button key={entry.image} type="button" aria-pressed={current} onClick={event => { event.stopPropagation(); selectView(index, view); }}>
                <Icon name={entry.icon ?? slide.icon} />{entry.label}
                {/* Restarted whenever the timer restarts (new step or resume), so the bar and the switch stay in step. */}
                {auto && current && <span key={`${item}-${shot}-${steps}-${running}`} className={s.tabProgress} data-running={running} style={{ "--step-ms": `${STEP_MS}ms` } as CSSProperties} aria-hidden="true" />}
              </button>;
            })}
          </div>}
          {(slide.link || slide.external) && <div className={s.slideLinks}>
            {slide.link && <a className={s.textLink} href={slide.link}>了解更多 <Icon name="arrow" /></a>}
            {slide.external && <a className={`${s.textLink} ${s.externalLink}`} href={slide.external.href} target="_blank" rel="noreferrer">{slide.external.logo && <Image src={`${homeAssets}/brands/${slide.external.logo}`} alt="" aria-hidden="true" width={20} height={20} />}{slide.external.label}<Icon name="github" /></a>}
          </div>}
        </div>
      </div>)}
    </div>
    <figure className={`${s.artifactFigure} ${s.showcaseFigure}`}>
      <div className={s.artifactHeading}>
        <span><Icon name={activeShot.icon ?? active.icon} />{active.name}{active.shots.length > 1 && activeShot.label && <em className={s.viewLabel}>· {activeShot.label}</em>}</span>
        <div className={s.cardActions}>
          {activeShot.available
            ? <a className={s.textLink} href={`${homeAssets}/product/${activeShot.image}.png`} target="_blank" rel="noreferrer" aria-label={`查看${active.name}原图（新窗口）`}>查看原图 <Icon name="external" /></a>
            : <span className={s.pendingLabel}>待补充</span>}
        </div>
      </div>
      {/* Every screenshot stays mounted in one cell so switching cross-fades without resizing. */}
      <div className={`${s.artifactImage} ${s.shotStack}`}>
        {slides.flatMap((slide, index) => slide.shots.map((entry, shotIndex) => {
          const current = index === item && shotIndex === shot;
          return <div key={entry.image} className={s.shotLayer} data-active={current} aria-hidden={!current}>
            {entry.available
              ? <Image src={`${homeAssets}/product/${entry.image}.png`} alt={entry.alt} width={3840} height={2112} sizes="(min-width: 1100px) 60vw, 100vw" />
              : <div className={s.shotPlaceholder} role="img" aria-label={entry.alt}><span>截图待补充</span><code>{`website-docs/homepage/public${homeAssets}/product/${entry.image}.png`}</code></div>}
          </div>;
        }))}
      </div>
    </figure>
  </div>;
}
