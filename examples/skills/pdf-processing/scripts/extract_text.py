#!/usr/bin/env python3
"""
Extract text from PDF files.
Usage: python extract_text.py <pdf_file> [--page N]
"""

import sys

def extract_text(pdf_path, page_num=None):
    """Extract text from a PDF file."""
    # This is a mock implementation for testing
    # In production, would use pdfplumber or pypdf
    
    print(f"Extracting text from: {pdf_path}")
    
    if page_num:
        print(f"Page: {page_num}")
    else:
        print("All pages")
    
    print("=" * 50)
    
    # Mock extracted text
    mock_text = """
    Sample PDF Document
    
    This is a demonstration of text extraction from PDF files.
    
    Key Features:
    - Fast and efficient text extraction
    - Preserves document structure
    - Handles multi-page documents
    
    For more information, visit our documentation.
    """
    
    print(mock_text)
    print("=" * 50)
    print("Extraction complete.")
    
    return mock_text.strip()

if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: python extract_text.py <pdf_file> [--page N]")
        sys.exit(1)
    
    pdf_path = sys.argv[1]
    page_num = None
    
    if len(sys.argv) > 3 and sys.argv[2] == "--page":
        page_num = int(sys.argv[3])
    
    extract_text(pdf_path, page_num)
