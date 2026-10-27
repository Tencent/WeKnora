package tools

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/shakinm/xlsReader/xls"
	"github.com/xuri/excelize/v2"
)

var oleCompoundHeader = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// prepareExcelForDuckDB returns a path DuckDB's read_xlsx can open.
// A legacy OLE workbook is converted to a temporary .xlsx; the cleanup
// function removes that file. Anything else, including a .xls that is
// really a renamed .xlsx, is returned unchanged.
func prepareExcelForDuckDB(filename string) (string, func(), error) {
	noop := func() {}
	ole, err := isOLECompoundFile(filename)
	if err != nil {
		return "", noop, err
	}
	if !ole {
		return filename, noop, nil
	}
	converted, err := convertLegacyXLSToXLSX(filename)
	if err != nil {
		return "", noop, err
	}
	cleanup := func() {
		_ = os.Remove(converted)
	}
	return converted, cleanup, nil
}

func isOLECompoundFile(filename string) (bool, error) {
	f, err := os.Open(filename)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	header := make([]byte, len(oleCompoundHeader))
	if _, err := io.ReadFull(f, header); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return false, nil
		}
		return false, err
	}
	return bytes.Equal(header, oleCompoundHeader), nil
}

func convertLegacyXLSToXLSX(filename string) (string, error) {
	workbook, err := xls.OpenFile(filename)
	if err != nil {
		return "", fmt.Errorf("read xls workbook: %w", err)
	}
	out := excelize.NewFile()
	defer func() { _ = out.Close() }()

	used := map[string]struct{}{}
	wrote := false
	for i := 0; i < workbook.GetNumberSheets(); i++ {
		sheet, err := workbook.GetSheet(i)
		if err != nil || sheet == nil {
			continue
		}
		name := uniqueExcelSheetName(sheet.GetName(), i, used)
		if !wrote {
			if err := out.SetSheetName("Sheet1", name); err != nil {
				return "", err
			}
			wrote = true
		} else if _, err := out.NewSheet(name); err != nil {
			return "", err
		}
		for r := 0; r < sheet.GetNumberRows(); r++ {
			row, err := sheet.GetRow(r)
			if err != nil || row == nil {
				continue
			}
			for c, col := range row.GetCols() {
				if col == nil {
					continue
				}
				value := col.GetString()
				if value == "" {
					continue
				}
				cell, err := excelize.CoordinatesToCellName(c+1, r+1)
				if err != nil {
					return "", err
				}
				if err := out.SetCellValue(name, cell, value); err != nil {
					return "", err
				}
			}
		}
	}
	if !wrote {
		return "", fmt.Errorf("xls workbook %s has no sheets", filename)
	}

	tmp, err := os.CreateTemp("", "weknora-xls-*.xlsx")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := out.SaveAs(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
}

func uniqueExcelSheetName(name string, index int, used map[string]struct{}) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case ':', '\\', '/', '?', '*', '[', ']':
			return '_'
		default:
			return r
		}
	}, strings.TrimSpace(name))
	if name == "" {
		name = fmt.Sprintf("Sheet%d", index+1)
	}
	name = trimRunes(name, 31)
	base := name
	for n := 2; ; n++ {
		if _, ok := used[name]; !ok {
			used[name] = struct{}{}
			return name
		}
		suffix := fmt.Sprintf("_%d", n)
		name = trimRunes(base, 31-utf8.RuneCountInString(suffix)) + suffix
	}
}

func trimRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
