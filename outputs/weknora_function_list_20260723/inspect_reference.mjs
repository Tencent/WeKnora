import fs from "node:fs/promises";
import { FileBlob, SpreadsheetFile } from "@oai/artifact-tool";

const sourcePath = "/Users/xiaoxie/Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files/wxid_xzs8ov31oohb22_d513/msg/file/2026-05/4.20标注_智联企客 2024 4.17 待评审功能清单.xlsx";
const outputDir = "/Users/xiaoxie/Desktop/Project/WeKnora/outputs/weknora_function_list_20260723";

const workbook = await SpreadsheetFile.importXlsx(await FileBlob.load(sourcePath));
const summary = await workbook.inspect({
  kind: "workbook,sheet,table",
  maxChars: 12000,
  tableMaxRows: 18,
  tableMaxCols: 16,
  tableMaxCellChars: 160,
});
await fs.writeFile(`${outputDir}/reference_summary.ndjson`, summary.ndjson, "utf8");

const sheets = workbook.worksheets.items;
const details = [];
for (const sheet of sheets) {
  const used = sheet.getUsedRange();
  details.push({
    name: sheet.name,
    address: used?.address ?? null,
    values: used?.values ?? [],
    formulas: used?.formulas ?? [],
  });
  if (used) {
    const preview = await workbook.render({
      sheetName: sheet.name,
      autoCrop: "all",
      scale: 1,
      format: "png",
    });
    const safe = sheet.name.replace(/[\\/:*?"<>|]/g, "_");
    await fs.writeFile(`${outputDir}/reference_${safe}.png`, new Uint8Array(await preview.arrayBuffer()));
  }
}
await fs.writeFile(`${outputDir}/reference_details.json`, JSON.stringify(details, null, 2), "utf8");
console.log(JSON.stringify({ sheets: details.map(({ name, address, values }) => ({ name, address, rows: values.length, cols: values[0]?.length ?? 0 })) }));
