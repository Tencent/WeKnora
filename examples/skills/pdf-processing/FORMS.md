# PDF Form Filling Guide

This guide covers how to fill PDF forms programmatically.

## Prerequisites

Install required packages:
```bash
pip install pypdf pdfrw
```

## Basic Form Filling

```python
from pypdf import PdfReader, PdfWriter

def fill_form(input_path, output_path, field_data):
    reader = PdfReader(input_path)
    writer = PdfWriter()
    
    # Clone the original PDF
    writer.clone_document_from_reader(reader)
    
    # Fill form fields
    for page in writer.pages:
        writer.update_page_form_field_values(page, field_data)
    
    # Save the filled PDF
    with open(output_path, "wb") as f:
        writer.write(f)
```

## Supported Field Types

- Text fields
- Checkboxes
- Radio buttons
- Dropdown lists

## Tips

1. Use `scripts/analyze_form.py` to discover available fields
2. Field names are case-sensitive
3. Always verify output after filling
