"use client";

import Image from "next/image";
import { useEffect, useRef, useState } from "react";
import { homeAssets } from "../../shared/header";
import { adopters, adopterIssue } from "./adopters";
import { Icon } from "./ui";
import s from "./home.module.css";
import a from "./adopters.module.css";

export function AdopterShowcase() {
  const root = useRef<HTMLElement>(null);
  const inner = useRef<HTMLDivElement>(null);
  const [manual, setManual] = useState(false);
  const [active, setActive] = useState<{ index: number; left: number } | null>(null);

  useEffect(() => {
    if (!active) return;
    const dismiss = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setActive(null);
    };
    document.addEventListener("pointerdown", dismiss);
    return () => document.removeEventListener("pointerdown", dismiss);
  }, [active]);

  function showDetails(index: number, button: HTMLButtonElement, keyboard = false) {
    if (!inner.current) return;
    const bounds = inner.current.getBoundingClientRect();
    const logo = button.getBoundingClientRect();
    const width = Math.min(340, bounds.width);
    const left = keyboard ? 0 : Math.max(0, Math.min(bounds.width - width, logo.left - bounds.left + (logo.width - width) / 2));
    setActive({ index, left });
  }

  return <section ref={root} id="adopters" className={a.showcase} data-tone="subtle" data-paused={active !== null} data-manual={manual} aria-labelledby="adopters-title"
    onPointerLeave={event => { if (event.pointerType === "mouse") setActive(null); }}
    onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget)) setActive(null); }}
    onKeyDown={event => { if (event.key === "Escape") setActive(null); }}>
    <div ref={inner} className={`${s.shell} ${a.inner}`}>
      <div className={a.heading} onPointerEnter={event => { if (event.pointerType === "mouse") setActive(null); }}>
        <h2 id="adopters-title">谁在使用 WeKnora</h2>
        <a className={a.join} href={adopterIssue} target="_blank" rel="noreferrer">分享你的实践 <Icon name="arrow" /></a>
      </div>
      <div id="adopter-logos" className={a.viewport}>
        <div className={a.track}>
          {/* Equal-width copies make the linear animation loop without a jump. */}
          {[0, 1].map(copy => <ul className={a.group} key={copy} aria-hidden={copy === 1 ? true : undefined} aria-label={copy === 0 ? "使用 WeKnora 的团队与产品" : undefined}>
            {adopters.map((adopter, index) => <li key={adopter.name}>
              <button className={a.logo} data-variant={adopter.variant} type="button" tabIndex={copy === 1 ? -1 : 0} aria-label={`了解${adopter.name}的应用场景`} aria-expanded={active?.index === index} aria-controls={`adopter-details-${index}`}
                onPointerDown={event => { if (copy === 1) event.preventDefault(); }}
                onPointerEnter={event => { if (event.pointerType === "mouse") showDetails(index, event.currentTarget); }}
                onFocus={event => { if (event.currentTarget.matches(":focus-visible")) { setManual(true); showDetails(index, event.currentTarget, true); } }}
                onClick={event => { if (active?.index === index) setActive(null); else showDetails(index, event.currentTarget); }}>
                <span className={a.logoMark}>
                  <Image src={`${homeAssets}/adopters/${adopter.logo}`} alt="" width={adopter.logoSize.width} height={adopter.logoSize.height} style={adopter.logoScale ? { transform: `scale(${adopter.logoScale})` } : undefined} />
                </span>
                {adopter.variant === "B" && <span className={a.logoName}>{adopter.name}</span>}
              </button>
            </li>)}
          </ul>)}
        </div>
      </div>
      {/* Outside the clipped marquee so details can float over the next section. */}
      {adopters.map((adopter, index) => <div key={adopter.name} id={`adopter-details-${index}`} className={a.details} hidden={active?.index !== index} style={{ left: active?.left ?? 0 }} role="region" aria-labelledby={`adopter-name-${index}`}>
        <h3 id={`adopter-name-${index}`}>{adopter.name}</h3>
        <ul className={a.tags} aria-label="行业与应用场景">
          <li className={a.industry}>{adopter.industry}</li>
          {adopter.useCases.map(useCase => <li key={useCase}>{useCase}</li>)}
        </ul>
        <p className={a.description}>{adopter.description}</p>
      </div>)}
    </div>
  </section>;
}
