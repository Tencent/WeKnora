# WeKnora homepage source

The Next.js homepage is one part of the unified site. Run `npm run setup`, `npm run build`, and `npm run preview` from the parent directory to include both the homepage and the complete `/docs/` site. Deployment instructions are in the parent README.

- `app/page.tsx`: product content and same-origin documentation links.
- `app/home.module.css`: homepage layout.
- `app/interactive.tsx`: responsive navigation, theme switch, and on-demand README video.
- `app/theme.ts`: VitePress-compatible theme persistence and pre-paint initialization.
- `../shared/brand.css`: shared light/dark design tokens.

`npm run dev` can still preview the homepage alone during development. `/docs/` requires the combined preview from the parent directory.
