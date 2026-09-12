# Public parser benchmark subset

`manifest-smoke.json` contains 8 pages; `manifest-full.json` contains the frozen 100-page subset. The smoke set is an exact subset of the full set. `sources.json` records official dataset and evaluator revisions. `evaluators-lock.json` records the URL and SHA-256 of each unmodified official evaluator source file.

Inputs, annotations, upstream code and run results are downloaded or generated under the ignored `artifacts/parser-benchmark/` directory. Source PDFs and annotations retain their original rights. OmniDocBench states research-only use; olmOCR-bench declares ODC-BY on its dataset card. Original documents may carry separate rights. This repository commits manifests and reproducible code, not original documents.

The complete procedure and metric definitions are in [the dataset methodology](../../docs/parser-benchmark-dataset.md).
