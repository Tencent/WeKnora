// Editable example, not a mandatory presentation template.
const pptxgen = require('pptxgenjs');
const { calcTextBox, warnIfSlideElementsOutOfBounds } = require('../assets/pptxgenjs_helpers');
async function main() {
  const pptx = new pptxgen();
  const font = 'Noto Sans CJK SC';
  pptx.layout = 'LAYOUT_WIDE';
  pptx.author = 'WeKnora';
  pptx.theme = { headFontFace: font, bodyFontFace: font, lang: 'zh-CN' };
  let slide = pptx.addSlide();
  slide.background = {color: '14263D'};
  slide.addText('文档能力 · Office skills', {x: .8, y: .7, w: 11.7, h: .4, fontFace: font, fontSize: 15, color: '89CEC9'});
  slide.addText('中文与 English 保持清晰、可编辑', {x: .8, y: 2, w: 11.7, h: 1.8, fontFace: font, fontSize: 38, bold: true, color: 'FFFFFF', breakLine: false});
  slide.addText('布局、字体与内容一起验证', {x: .85, y: 5.7, w: 10, h: .6, fontFace: font, fontSize: 20, color: 'C5D6E8'});
  slide = pptx.addSlide();
  slide.background = {color: 'F4F6F9'};
  slide.addText('示例数据：对比应当一眼可读', {x: .7, y: .5, w: 11.9, h: .65, fontFace: font, fontSize: 29, bold: true, color: '14263D'});
  slide.addChart(pptx.ChartType.bar, [{name: '示例', labels: ['方案 A', '方案 B', '方案 C'], values: [42, 65, 80]}], {
    x: .8, y: 1.7, w: 7.4, h: 4.7, catAxisLabelFontFace: font, valAxisLabelFontFace: font,
    catAxisLabelFontSize: 16, valAxisLabelFontSize: 12, showLegend: false, showValue: true,
    chartColors: ['278C89'], showTitle: false, showCatName: false, showBorder: false
  });
  const text = '数据是示例，不代表真实评测。\n用原生图表保留后续编辑能力。';
  slide.addText(text, {...calcTextBox(20, {text, w: 3.5, fontFace: font}), x: 9, y: 2.3, w: 3.5, fontFace: font, fontSize: 20, color: '354A62'});
  for (const s of pptx._slides) warnIfSlideElementsOutOfBounds(s, pptx);
  await pptx.writeFile({fileName: process.argv[2]});
}
main().catch(error => { console.error(error); process.exitCode = 1; });
