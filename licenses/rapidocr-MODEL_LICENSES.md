# RapidOCR Model License and Attribution Notice

This document records the license, copyright attribution, provenance, and
redistribution requirements for OCR model artifacts distributed by RapidOCR.
It does not replace or modify the Apache License, Version 2.0.

## 1. Separate licensed works

RapidOCR contains two separately copyrighted categories of work:

1. **Source code and engineering components.** Copyright is held by the
   RapidOCR Authors. These components are licensed under the Apache License,
   Version 2.0, as described in the RapidOCR project `LICENSE` file.
2. **OCR model artifacts.** Copyright in the upstream model weights is held by
   Baidu and/or the applicable PaddleOCR rights holders. RapidOCR does not
   claim ownership of the upstream model weights.

Both categories are distributed under the Apache License, Version 2.0, but
their copyright ownership and provenance remain separate.

## 2. Scope of this notice

This notice applies to the model artifacts listed in Section 6. Other model
artifacts available through RapidOCR may have different upstream versions or
terms and must be reviewed against their own source records before being
redistributed.

This notice does not automatically cover third-party models, datasets, fonts,
sample images, inference runtimes, or other assets that are not derived from
official PaddleOCR models. Those works remain subject to their own applicable
terms.

## 3. Upstream attribution and licensing evidence

The models in Section 6 are released by, or derived from models released by,
the PaddleOCR project. PaddleOCR is released under the Apache License, Version
2.0. Official PaddlePaddle model cards identify the PP-OCRv6 model weights and
ONNX representations as `Apache-2.0`. PaddleOCR's official model list
identifies the legacy text angle classification model and its official
distribution source.

Upstream references:

- PaddleOCR project: <https://github.com/PaddlePaddle/PaddleOCR>
- PaddleOCR license: <https://github.com/PaddlePaddle/PaddleOCR/blob/main/LICENSE>
- PaddleOCR model list: <https://github.com/PaddlePaddle/PaddleOCR/blob/main/docs/version3.x/model_list.md>
- PaddleOCR ONNX conversion documentation: <https://paddlepaddle.github.io/PaddleOCR/latest/en/version3.x/inference_deployment/others/obtaining_onnx_models.html>
- Official PP-OCRv6 small detection ONNX model card: <https://huggingface.co/PaddlePaddle/PP-OCRv6_small_det_onnx>
- Official PP-OCRv6 small recognition ONNX model card: <https://huggingface.co/PaddlePaddle/PP-OCRv6_small_rec_onnx>
- Official legacy text angle classification model list: <https://github.com/PaddlePaddle/PaddleOCR/blob/main/docs/version2.x/ppocr/model_list.en.md>

The legacy `ch_ppocr_mobile_v2.0_cls` model predates the official Hugging Face
model hosting and is identified through PaddleOCR's official model list and
model download service.

## 4. Applicable license

The model artifacts in Section 6 are made available under the Apache License,
Version 2.0 (`Apache-2.0`). The complete license text is available at
<https://www.apache.org/licenses/LICENSE-2.0>.

Subject to the conditions of Apache-2.0, the license permits use,
reproduction, modification, preparation of derivative works, public display,
sublicensing, and distribution in source or object form. These permissions are
not restricted to non-commercial use.

The license applies only to rights that the relevant licensors are authorized
to grant. No additional model-specific restriction has been identified for the
PaddleOCR-derived artifacts covered by this notice.

## 5. Converted model artifacts

RapidOCR converts or repackages upstream PaddleOCR inference models for use
with supported inference engines. These conversions are mechanical
transformations of the model representation. Conversion does not transfer
ownership of the upstream weights to RapidOCR and does not remove the upstream
copyright or attribution.

RapidOCR distributes the converted artifacts in Section 6 under the same
Apache-2.0 terms that apply to the corresponding upstream PaddleOCR models. The
conversion and packaging scripts, configuration, and RapidOCR-authored
metadata are separately copyrighted by the RapidOCR Authors and licensed under
Apache-2.0 as part of the RapidOCR source code.

A RapidOCR-converted artifact may not be byte-for-byte identical to an ONNX or
other representation hosted by PaddlePaddle. Artifact identity must therefore
be established using its source record and cryptographic hash rather than its
filename alone.

## 6. Bundled model artifacts

The following artifacts are covered by this notice and are distributed with
RapidOCR's default ONNX Runtime configuration:

| RapidOCR artifact | Upstream model | SHA-256 | RapidOCR source |
| --- | --- | --- | --- |
| `PP-OCRv6_det_small.onnx` | `PP-OCRv6_small_det` | `090f04abcd9d9a7498bc4ebf677e4cb9bdce1fe4197ddb7e529f1ef44e1ff94f` | [ModelScope](https://www.modelscope.cn/models/RapidAI/RapidOCR/resolve/v3.9.2/onnx/PP-OCRv6/det/PP-OCRv6_det_small.onnx) |
| `PP-OCRv6_rec_small.onnx` | `PP-OCRv6_small_rec` | `6f327246b50388f3c176ae304bd95767ea6dc0c9ae92153ef8cbe210b3c14884` | [ModelScope](https://www.modelscope.cn/models/RapidAI/RapidOCR/resolve/v3.9.2/onnx/PP-OCRv6/rec/PP-OCRv6_rec_small.onnx) |
| `ch_ppocr_mobile_v2.0_cls_mobile.onnx` | `ch_ppocr_mobile_v2.0_cls` | `e47acedf663230f8863ff1ab0e64dd2d82b838fceb5957146dab185a89d6215c` | [ModelScope](https://www.modelscope.cn/models/RapidAI/RapidOCR/resolve/v3.9.2/onnx/PP-OCRv4/cls/ch_ppocr_mobile_v2.0_cls_mobile.onnx) |

The model registry in `rapidocr/default_models.yaml` is the authoritative
release record for the source URL and SHA-256 value of each supported model
artifact.

## 7. Redistribution requirements

When redistributing a covered model artifact, or a modified or converted form
of one, comply with Section 4 of Apache-2.0. In particular, you must:

1. Give recipients a copy of the Apache License, Version 2.0.
2. Cause modified files to carry prominent notices stating that you changed
   them.
3. Retain applicable copyright, patent, trademark, and attribution notices
   from the upstream work, except notices that do not pertain to the
   redistributed work.
4. If an upstream distribution contains a `NOTICE` file, include the relevant
   contents of that notice in a permitted form.

At the time of publication, RapidOCR has not identified an additional
model-specific `NOTICE` file for the artifacts listed in Section 6. If a future
upstream release supplies one, its applicable contents must be retained.

For converted files, an appropriate modification notice is:

> Converted from an official PaddleOCR model to the indicated inference format
> and packaged for use with RapidOCR. The upstream model weights are copyright
> Baidu and/or the applicable PaddleOCR rights holders and are licensed under
> the Apache License, Version 2.0.

## 8. Trademarks and endorsement

Apache-2.0 does not grant permission to use the trade names, trademarks,
service marks, or product names of Baidu, PaddlePaddle, PaddleOCR, or RapidOCR,
except as required for reasonable and customary description of the origin of
the model artifacts. Redistribution must not imply endorsement, sponsorship,
or affiliation without separate permission.

## 9. Disclaimer

Unless required by applicable law or agreed to in writing, the model artifacts
are provided on an **AS IS** basis, without warranties or conditions of any
kind. The warranty disclaimer and limitation of liability in Apache-2.0 apply.

Model accuracy, fitness, security, and regulatory suitability remain the
responsibility of the user and downstream distributor. RapidOCR is not a
medical device, and neither the model license nor this notice represents a
certification or guarantee for medical, safety-critical, or regulated use.

## 10. Release maintenance

When a RapidOCR release adds or changes a model artifact, maintainers should:

1. Verify the upstream model and its applicable license.
2. Record the upstream source and immutable version or revision where
   available.
3. Record the conversion tool, tool version, model format, and conversion
   options where available.
4. Record and verify the artifact SHA-256 value.
5. Preserve any applicable upstream copyright, attribution, and `NOTICE`
   material.
6. Update this notice when the covered model families or applicable terms
   change.
