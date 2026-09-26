package paperless

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// paperlessTime accepts timestamps and date-only values returned by Paperless.
type paperlessTime struct {
	time.Time
}

func (t *paperlessTime) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("null")) || bytes.Equal(data, []byte(`""`)) {
		t.Time = time.Time{}
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value == "" {
		t.Time = time.Time{}
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		parsed, err = time.Parse("2006-01-02", value)
	}
	if err != nil {
		return fmt.Errorf("parse Paperless date %q: %w", value, err)
	}
	t.Time = parsed
	return nil
}

type listResponse[T any] struct {
	Next    string `json:"next"`
	Results []T    `json:"results"`
}

type correspondent struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type documentType struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type customField struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	DataType string `json:"data_type"`
}

type customFieldValue struct {
	Field int         `json:"field"`
	Value interface{} `json:"value"`
}

type document struct {
	ID               int                `json:"id"`
	Title            string             `json:"title"`
	Content          string             `json:"content"`
	Created          paperlessTime      `json:"created"`
	Added            paperlessTime      `json:"added"`
	Modified         paperlessTime      `json:"modified"`
	OriginalFileName string             `json:"original_file_name"`
	ArchiveSerial    string             `json:"archive_serial_number"`
	Correspondent    int                `json:"correspondent"`
	DocumentType     int                `json:"document_type"`
	CustomFields     []customFieldValue `json:"custom_fields"`
}
