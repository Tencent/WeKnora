package mattermost

import (
	"encoding/json"
	"fmt"
	"net/url"
)

func parseFormBody(body []byte) (url.Values, error) {
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, fmt.Errorf("parse form: %w", err)
	}
	return values, nil
}

func jsonArrayFromCSV(csv string) []byte {
	parts := splitFileIDs(csv)
	if len(parts) == 0 {
		return []byte("[]")
	}
	b, err := json.Marshal(parts)
	if err != nil {
		return []byte("[]")
	}
	return b
}
