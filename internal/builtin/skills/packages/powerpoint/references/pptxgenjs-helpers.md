# PptxGenJS Helpers

## When To Read This

Read this file when you need helper API details, command examples for the bundled Python scripts, or dependency notes for a slide-generation task.

## Helper Modules

- `autoFontSize(textOrRuns, fontFace, opts)`: Pick a font size that fits a fixed box.
- `calcTextBox(fontSizePt, opts)`: Estimate text-box geometry from font size and content.
- `calcTextBoxHeightSimple(fontSizePt, numLines, leading?, padding?)`: Quick text height estimate.
- `imageSizingCrop(pathOrData, x, y, w, h)`: Center-crop an image into a target box.
- `imageSizingContain(pathOrData, x, y, w, h)`: Fit an image fully inside a target box.
- `svgToDataUri(svgString)`: Convert an SVG string into an embeddable data URI.
- `latexToSvgDataUri(texString)`: Render LaTeX to SVG for crisp equations.
- `getImageDimensions(pathOrData)`: Read image width, height, type, and aspect ratio.
- `safeOuterShadow(...)`: Build a safe outer-shadow config for PowerPoint output.
- `codeToRuns(source, language)`: Convert source code into rich-text runs for `addText`.
- `warnIfSlideHasOverlaps(slide, pptx)`: Emit overlap warnings for diagnostics.
- `warnIfSlideElementsOutOfBounds(slide, pptx)`: Emit boundary warnings for diagnostics.
- `alignSlideElements(slide, indices, alignment)`: Align selected elements precisely.
- `distributeSlideElements(slide, indices, direction)`: Evenly space selected elements.

## Dependency Notes

JavaScript helpers expect these packages when you use the corresponding features:

- Core authoring: `pptxgenjs`
- Text measurement: `skia-canvas`, `linebreak`, `fontkit`
- Syntax highlighting: `prismjs`
- LaTeX rendering: `mathjax-full`

Python scripts expect these packages:

- `Pillow`
- `pdf2image`
- `python-pptx`
- `numpy`

System tools used by the Python scripts:

- `soffice` / LibreOffice for PPTX to PDF conversion
- Poppler tools for PDF size/raster support used by `pdf2image`
- `fc-list` for font inspection
- Optional rasterization tools for `ensure_raster_image.py`: Inkscape, ImageMagick, Ghostscript, `heif-convert`, `JxrDecApp`

## Script Notes

- `render_slides.py`: Convert a deck to PNGs. Good for visual review and diffing.
- `slides_test.py`: Add a gray border outside the original canvas, render, and check whether any content leaks into the border.
- `create_montage.py`: Combine multiple rendered slide images into a single overview image.
- `detect_font.py`: Distinguish between fonts that are missing entirely and fonts that are installed but substituted during rendering.
- `ensure_raster_image.py`: Produce a PNG from common vector or unusual raster formats so you can inspect or place the asset easily.

## Practical Rules

- Default to `LAYOUT_WIDE` unless the source material says otherwise.
- Set font families explicitly before measuring text.
- Use `valign: "top"` for content boxes that may grow.
- Prefer native PowerPoint charts over rendered images when the chart is simple and likely to be edited later.
- Use SVG instead of PNG for diagrams whenever possible.
