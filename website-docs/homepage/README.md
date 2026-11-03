# WeKnora homepage source

The Next.js homepage is one part of the unified site. Run `npm run setup`, `npm run build`, and `npm run preview` from the parent directory to include both the homepage and the complete `/docs/` site. Deployment instructions are in the parent README.

- `app/page.tsx`: product content and same-origin documentation links.
- `app/home.module.css`: homepage layout.
- `app/interactive.tsx`: responsive navigation, theme switch, and on-demand README video.
- `app/adopters.ts`: the eight teams/products from the supplied brand pack, with adoption stages, use cases, and public links retained as metadata. `variant: "A"` centers a horizontal logo alone; `variant: "B"` places a square logo beside the company/product name. `logoSize` records the source dimensions; optional `logoScale` trims excessive blank canvas in the display without changing the artwork. 赛乐医疗 uses A and 智达创客 uses B, correcting their reversed pack settings. Honor the supplied roster; do not add other issue submissions to the display. Original logos live in `public/docs/_home/adopters/`. Sources are recorded in `BRAND-ASSETS.md`.
- `app/adopter-showcase.tsx`, `app/adopters.module.css`: a compact logo strip below the hero. Two equal-width copies form a continuous linear CSS loop; hovering the logos pauses the strip and reveals a floating detail panel, while touch users tap a logo. The heading and contribution link do not pause motion. Details close on pointer exit, outside tap, or Escape, without pause or close buttons. Cards show the brand name, industry and use-case tags, then a short description. Adoption stages and public links are retained as curated metadata but are not displayed in the cards; submission sources remain in `BRAND-ASSETS.md`. Keyboard and reduced-motion users get one stationary, horizontally scrollable row. Duplicate logos are excluded from the tab order and accessibility tree; details sit outside the clipped strip.
- `app/theme.ts`: VitePress-compatible theme persistence and pre-paint initialization.
- `../shared/brand.css`: shared light/dark design tokens.
- `public/docs/_home/`: homepage images. The site serves only `/` and `/docs/`, so build output and public assets both live under `/docs/_home/` (`homeAssets` in `../shared/header.ts`).

`npm run dev` can still preview the homepage alone during development. `/docs/` requires the combined preview from the parent directory.
