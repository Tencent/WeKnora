package tools

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
)

// filterEmptyExcelSheets checks cell content rather than DuckDB's inferred row
// count: header-only sheets and uncached formulas must keep their columns.
func filterEmptyExcelSheets(ctx context.Context, filename string, names []string) ([]string, error) {
	book, err := zip.OpenReader(filename)
	if err != nil {
		return nil, err
	}
	defer func() { _ = book.Close() }()
	var workbook struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			ID   string `xml:"id,attr"`
		} `xml:"sheets>sheet"`
	}
	var relationships struct {
		Items []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
			Mode   string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	readXML := func(name string, out any) error {
		r, openErr := book.Open(name)
		if openErr != nil {
			return openErr
		}
		decodeErr := xml.NewDecoder(r).Decode(out)
		closeErr := r.Close()
		if decodeErr != nil {
			return decodeErr
		}
		return closeErr
	}
	if err := readXML("xl/workbook.xml", &workbook); err != nil {
		return nil, err
	}
	if err := readXML("xl/_rels/workbook.xml.rels", &relationships); err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	wantedIDs := make(map[string]bool, len(names))
	for _, sheet := range workbook.Sheets {
		if wanted[sheet.Name] {
			wantedIDs[sheet.ID] = true
		}
	}
	targets := make(map[string]string, len(relationships.Items))
	for _, rel := range relationships.Items {
		if !wantedIDs[rel.ID] || rel.Mode == "External" || rel.Target == "" {
			continue
		}
		target, err := url.Parse(rel.Target)
		if err != nil {
			return nil, fmt.Errorf("invalid worksheet relationship: %w", err)
		}
		if target.IsAbs() || target.Host != "" {
			continue
		}
		if strings.HasPrefix(target.Path, "/") {
			targets[rel.ID] = strings.TrimPrefix(target.Path, "/")
		} else {
			targets[rel.ID] = path.Join("xl", target.Path)
		}
	}
	sheets := make(map[string]string, len(workbook.Sheets))
	for _, sheet := range workbook.Sheets {
		sheets[sheet.Name] = targets[sheet.ID]
	}
	kept := make([]string, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, err := book.Open(sheets[name])
		if err != nil {
			return nil, fmt.Errorf("open worksheet %q: %w", name, err)
		}
		hasContent, scanErr := excelSheetHasContent(ctx, r)
		closeErr := r.Close()
		if scanErr != nil {
			return nil, fmt.Errorf("read worksheet %q: %w", name, scanErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if hasContent {
			kept = append(kept, name)
		}
	}
	return kept, nil
}

// Stop at the first value or formula; only a fully read, well-formed worksheet
// can be classified as empty. Shared-string indices count as content without
// loading the shared-string table. Formatting and dimensions alone do not.
func excelSheetHasContent(ctx context.Context, r io.Reader) (bool, error) {
	decoder := xml.NewDecoder(r)
	var stack []string
	cellDepth := 0
	seenRoot := false
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			if !seenRoot {
				return false, fmt.Errorf("missing worksheet element")
			}
			return false, nil
		}
		if err != nil {
			return false, err
		}
		switch v := token.(type) {
		case xml.StartElement:
			if len(stack) == 0 {
				if seenRoot || v.Name.Local != "worksheet" {
					return false, fmt.Errorf("unexpected worksheet root %q", v.Name.Local)
				}
				seenRoot = true
			}
			stack = append(stack, v.Name.Local)
			if len(stack) == 4 && stack[1] == "sheetData" && stack[2] == "row" && stack[3] == "c" {
				cellDepth = len(stack)
			}
			if cellDepth != 0 && v.Name.Local == "f" {
				return true, nil
			}
		case xml.CharData:
			if len(stack) == 0 && strings.TrimSpace(string(v)) != "" {
				return false, fmt.Errorf("text outside worksheet element")
			}
			if cellDepth != 0 && len(v) != 0 && (stack[len(stack)-1] == "v" || stack[len(stack)-1] == "t") {
				return true, nil
			}
		case xml.EndElement:
			if len(stack) == cellDepth {
				cellDepth = 0
			}
			stack = stack[:len(stack)-1]
		}
	}
}
