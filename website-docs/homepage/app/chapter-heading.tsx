import type { ReactNode } from "react";
import s from "./home.module.css";

export function ChapterHeading({ id, label, children }: { id: string; label: string; children: ReactNode }) {
  return <div>
    <p className={`${s.eyebrow} ${s.chapterLabel}`}>{label}</p>
    <h2 id={id}>{children}</h2>
  </div>;
}
