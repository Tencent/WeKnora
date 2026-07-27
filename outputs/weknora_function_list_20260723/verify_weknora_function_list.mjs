import fs from "node:fs/promises";
import { FileBlob, SpreadsheetFile } from "@oai/artifact-tool";

const outputDir = "/Users/xiaoxie/Desktop/Project/WeKnora/outputs/weknora_function_list_20260723";
const path = `${outputDir}/WeKnora_功能清单_v0.7.0.xlsx`;
const workbook = await SpreadsheetFile.importXlsx(await FileBlob.load(path));

const checks = {};
checks.summaryTop = (await workbook.inspect({
  kind: "table",
  range: "汇总!A1:E20",
  include: "values,formulas",
  tableMaxRows: 20,
  tableMaxCols: 5,
  maxChars: 9000,
})).ndjson;
checks.summaryBottom = (await workbook.inspect({
  kind: "table",
  range: "汇总!A140:E156",
  include: "values,formulas",
  tableMaxRows: 20,
  tableMaxCols: 5,
  maxChars: 9000,
})).ndjson;
checks.sources = (await workbook.inspect({
  kind: "table",
  range: "来源说明!A1:D14",
  include: "values,formulas",
  tableMaxRows: 16,
  tableMaxCols: 4,
  maxChars: 9000,
})).ndjson;
checks.errors = (await workbook.inspect({
  kind: "match",
  searchTerm: "#REF!|#DIV/0!|#VALUE!|#NAME\\?|#N/A",
  options: { useRegex: true, maxResults: 300 },
  summary: "final formula error scan",
})).ndjson;

const summaryPreview = await workbook.render({ sheetName: "汇总", range: "A1:E156", scale: 1, format: "png" });
await fs.writeFile(`${outputDir}/verified_summary.png`, new Uint8Array(await summaryPreview.arrayBuffer()));
const sourcePreview = await workbook.render({ sheetName: "来源说明", autoCrop: "all", scale: 1, format: "png" });
await fs.writeFile(`${outputDir}/verified_sources.png`, new Uint8Array(await sourcePreview.arrayBuffer()));
await fs.writeFile(`${outputDir}/verification.json`, JSON.stringify(checks, null, 2), "utf8");
console.log(JSON.stringify({ errorScan: checks.errors, output: path }));
