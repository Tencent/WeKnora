---
name: pdf-processing
description: Extract text and tables from PDF files, fill forms, merge documents. Use when working with PDF files or when the user mentions PDFs, forms, or document extraction.
---
# PDF Processing

This skill provides utilities for working with PDF documents.

## Quick Start

Use pdfplumber to extract text from PDFs:

```python
import pdfplumber

with pdfplumber.open("document.pdf") as pdf:
    text = pdf.pages[0].extract_text()
    print(text)
```

## Available Operations

1. **Text Extraction**: Extract text content from PDF pages
2. **Table Extraction**: Extract tabular data from PDFs
3. **Form Filling**: Fill PDF forms with provided data
4. **Document Merging**: Combine multiple PDFs into one

## Advanced Features

**Form filling**: See [FORMS.md](FORMS.md) for complete guide

**Utility scripts**: 
- Run `scripts/analyze_form.py` to extract form fields
- Run `scripts/extract_text.py` to extract text from a PDF

## Best Practices

1. Always validate PDF files before processing
2. Handle password-protected PDFs gracefully
3. Check for scanned PDFs that may require OCR
