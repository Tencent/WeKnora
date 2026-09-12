# Sources and Version Snapshot

Research refreshed 2026-07-23 with `parallel-cli search` and `parallel-cli extract`. API and publisher requirements below use current official project, standards-body, publisher, or journal sources only. “Accessed” is 2026-07-23 unless another date is stated.

## Tested direct package snapshot

- **Matplotlib 3.11.1**, released 2026-07-18; Python >=3.11 [MPL-PYPI].
- **Seaborn 0.13.2**, released 2024-01-25; Python >=3.8 [SEABORN-PYPI].
- **Plotly 6.9.0**, released 2026-07-09; Python >=3.8 [PLOTLY-PYPI].
- **Kaleido 1.3.0**, released 2026-05-04 [KALEIDO-PYPI].
- **Pillow 12.3.0**, released 2026-07-01; Python >=3.10 [PIL-PYPI].
- **pypdf 6.14.2**, released 2026-06-23; Python >=3.9 [PYPDF-PYPI].

These are pinned direct-dependency snapshots used for smoke tests, not a transitive lock.

## Matplotlib

- **[MPL-PYPI]** [matplotlib on PyPI](https://pypi.org/project/matplotlib/) — current package version and release history; page dated 2026-07-18.
- **[MPL-RELEASE]** [Matplotlib release notes](https://matplotlib.org/stable/release/release_notes.html) — 3.11 release/API changes.
- **[MPL-SAVE]** [`matplotlib.figure.Figure.savefig`](https://matplotlib.org/stable/api/_as_gen/matplotlib.figure.Figure.savefig.html) — 3.11.1 signature; format inference, DPI, metadata, bounding boxes, transparency, backends, Pillow kwargs; built 2026-07-18.
- **[MPL-BACKENDS]** [Backends](https://matplotlib.org/stable/users/explain/figure/backends.html) — interactive versus static renderers; PDF/PS/SVG/PGF/Cairo formats.
- **[MPL-STYLE]** [Customizing Matplotlib with style sheets and rcParams](https://matplotlib.org/stable/users/explain/customizing.html) — `rc_context`, style composition, save settings, PDF/PS/SVG font types; built 2026-07-18.
- **[MPL-LAYOUT]** [Constrained layout guide](https://matplotlib.org/stable/users/explain/axes/constrainedlayout_guide.html) — `layout="constrained"`, colorbars, subfigures, GridSpec, interaction with `tight_layout`.
- **[MPL-GRIDSPEC]** [`matplotlib.gridspec`](https://matplotlib.org/stable/api/gridspec_api.html) — current grid layout API.
- **[MPL-NORM]** [Colormap normalization](https://matplotlib.org/stable/users/explain/colors/colormapnorms.html) — `Normalize`, `LogNorm`, `CenteredNorm`, `SymLogNorm`, `PowerNorm`, `BoundaryNorm`, `TwoSlopeNorm`; built 2026-07-18.
- **[MPL-CMAP]** [Choosing colormaps](https://matplotlib.org/stable/users/explain/colors/colormaps.html) — data classes and perceived lightness.

## Seaborn

- **[SEABORN-PYPI]** [seaborn on PyPI](https://pypi.org/project/seaborn/) — 0.13.2 package metadata and release history.
- **[SEABORN-ERROR]** [Statistical estimation and error bars](https://seaborn.pydata.org/tutorial/error_bars.html) — current `errorbar` methods, callable intervals, bootstrapping, `seed`, and `n_boot`.
- **[SEABORN-FAQ]** [Frequently asked questions](https://seaborn.pydata.org/faq.html) — axes-level versus figure-level functions, Matplotlib object-oriented integration, DPI/SVG notes.
- **[SEABORN-PALETTE]** [Choosing color palettes](https://seaborn.pydata.org/tutorial/color_palettes.html) — qualitative, sequential, and diverging palette APIs.
- **[SEABORN-THEME]** [`seaborn.set_theme`](https://seaborn.pydata.org/generated/seaborn.set_theme.html) — style, context, palette, font, scale, and rc parameters.

## Plotly and Kaleido

- **[PLOTLY-PYPI]** [plotly on PyPI](https://pypi.org/project/plotly/) — 6.9.0 package metadata; released 2026-07-09.
- **[PLOTLY-STATIC]** [Static image export in Python](https://plotly.com/python/static-image-export/) — Kaleido/Chrome setup, formats, `write_image`, `write_images`, dimensions/scale, WebGL rasterization, offline assets, defaults, EPS/Orca/engine deprecations; page dated 2026.
- **[PLOTLY-HTML]** [Interactive HTML export](https://plotly.com/python/interactive-html-export/) — `write_html`, `to_html`, `include_plotlyjs`, `full_html`; page dated 2026.
- **[PLOTLY-CHANGES]** [Static image generation changes in Plotly.py 6.1](https://plotly.com/python/static-image-generation-changes/) — Kaleido v1 migration and deprecations.
- **[KALEIDO]** [Plotly Kaleido repository](https://github.com/plotly/Kaleido) — Chrome requirement, v1 migration, direct APIs, and offline/page behavior.
- **[KALEIDO-PYPI]** [kaleido on PyPI](https://pypi.org/project/kaleido/) — 1.3.0 package metadata; released 2026-05-04.

## Accessibility and color

- **[WCAG22]** [Web Content Accessibility Guidelines (WCAG) 2.2](https://www.w3.org/TR/WCAG22/) — W3C Recommendation; normative SC 1.1.1, 1.4.1, 1.4.3, 1.4.5, and 1.4.11.
- **[WCAG-NONTEXT]** [Understanding SC 1.4.11: Non-text Contrast](https://www.w3.org/WAI/WCAG22/Understanding/non-text-contrast.html) — informative chart/graph examples and testing principles; not itself normative.
- **[WCAG-COLOR]** [Understanding SC 1.4.1: Use of Color](https://www.w3.org/WAI/WCAG22/Understanding/use-of-color.html) — informative non-color cue guidance.
- **[COLORBREWER]** [ColorBrewer 2.0](https://colorbrewer2.org/) — Cynthia Brewer, Mark Harrower, and Penn State; scheme type, data-class count, colorblind/print/photocopy filters, and exports.
- **[TOL-HOME]** [Paul Tol’s Notes](https://sronpersonalpages.nl/~pault/) — canonical site; page states the move from SRON on 2026-07-07.
- **[TOL]** [Paul Tol, “Colour Schemes”](https://sronpersonalpages.nl/~pault/data/colourschemes.pdf) — SRON/EPS/TN/09-002, issue 3.2, 2021-08-18; exact sRGB palettes, intended uses, color-vision checks, and grayscale analysis.
- **[WONG]** [Bang Wong, “Color blindness”](https://www.nature.com/articles/nmeth.1618) — Nature Methods 8, 441 (2011); source commonly used for the eight-color palette.

## Publishers and journals

All rules were accessed 2026-07-23. Pages without a displayed update date are labeled by access date rather than assigning an invented publication date.

- **[NATURE-FINAL]** [`Nature` final submission](https://www.nature.com/nature/for-authors/final-submission) — flagship final files, dimensions, fonts, formats, raster resolution, RGB/CMYK, Extended Data distinctions.
- **[NATURE-FIG]** [`Nature` research figure specifications](https://research-figure-guide.nature.com/figures/preparing-figures-our-specifications) — graphs, accessibility, RGB, 300/450 dpi discussion, editable Type 42 text, export.
- **[SCIENCE-INITIAL]** [`Science` initial manuscript instructions](https://www.science.org/content/page/instructions-preparing-initial-manuscript) — initial figure embedding, 300 dpi, widths, fonts, color/contrast, source data.
- **[SCIENCE-REVISED]** [`Science` revised manuscript instructions](https://www.science.org/content/page/instructions-preparing-revised-manuscript) — separate files, formats, minimum resolution, dimensions, no upsampling.
- **[CELL-FIG]** [Cell Press figure guidelines](https://www.cell.com/information-for-authors/figure-guidelines) — initial versus final stages, formats, widths, file size, DPI, RGB, fonts, image integrity, AI-assisted image policy.
- **[PLOS-FIG]** [PLOS Computational Biology figures](https://journals.plos.org/ploscompbiol/s/figures) — provisional-accept waiver, TIFF/EPS, dimensions, 300-600 dpi, RGB/grayscale, file size, image integrity, 2026-04-01 blot/gel requirement.
- **[ELSEVIER-FORMAT]** [Elsevier artwork formats checklist](https://www.elsevier.com/about/policies-and-standards/author/artwork-and-media-instructions/artwork-formats-checklist) — general formats, RGB preference, separate files, journal override.
- **[ELSEVIER-SIZE]** [Elsevier artwork sizing](https://www.elsevier.com/about/policies-and-standards/author/artwork-and-media-instructions/artwork-sizing) — general widths, 300/500/1,000 dpi, typography, and explicit journal variability.
- **[IEEE-SIZE]** [IEEE Resolution and Size](https://journals.ieeeauthorcenter.ieee.org/create-your-ieee-journal-article/create-graphics-for-your-article/resolution-and-size/) — modified 2025-02-25; PS/EPS/PDF, >300/>600 dpi, 88.9/182 mm.
- **[BMC-BIOINFO]** [BMC Bioinformatics: preparing your manuscript](https://bmcbioinformatics.biomedcentral.com/submission-guidelines/preparing-your-manuscript) — journal-specific formats, 85/170 mm, approximately 300 dpi, 10 MB, embedded fonts.
- **[ACS-GRAPHICS]** [ACS Preparing Manuscript Graphics](https://pubs.acs.org/page/4authors/submission/graphics_prep.html) — general dimensions and typography; no page update date displayed.

## Optional inspection backends

- **[PIL-PYPI]** [Pillow on PyPI](https://pypi.org/project/Pillow/) — 12.3.0 package metadata; released 2026-07-01.
- **[PYPDF-PYPI]** [pypdf on PyPI](https://pypi.org/project/pypdf/) — 6.14.2 package metadata; released 2026-06-23.

No Parallel JSON research artifacts are stored in this skill.
