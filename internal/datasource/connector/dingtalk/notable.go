package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// maxBaseRecords bounds how many records of one table are ingested. It sits
	// far above the size a single table is expected to reach, and exists so one
	// runaway table cannot make a sync unbounded. Hitting it is reported, never
	// silent.
	maxBaseRecords = 10_000

	// notableDateLocation is the zone DingTalk stores notable date columns in.
	// Date-only cells arrive as epoch milliseconds at local midnight in UTC+8,
	// so a 2026-01-31 cell is the instant 2026-01-30T16:00Z. Rendering that
	// instant in UTC would print the wrong day, so the tenant-independent
	// offset the product uses is applied explicitly.
	notableDateOffset = 8 * 60 * 60
)

// notableDateLocation is the fixed zone notable date cells are rendered in.
var notableDateLocation = time.FixedZone("DingTalk", notableDateOffset)

// baseHeadingText collapses inline whitespace so a Base or table name can never
// break out of its markdown heading, and escapes it like every other rendered
// title. It applies the connector's one rule for a heading to the names the
// notable API reports.
func baseHeadingText(value string) string {
	return escapeText(strings.Join(strings.Fields(value), " "))
}

// renderBase renders one multi-dimensional table (able) as a single markdown
// document: a title, then one section per table holding that table's records.
//
// A Base is one document to its users and one FetchedItem maps to one knowledge
// entry, so every table is combined here. The title is the Base ID because no
// API returns a Base's name: a Base is addressed by an id that cannot be derived
// from, or resolved through, the wiki node tree.
func renderBase(
	ctx context.Context,
	api dingTalkAPI,
	baseID string,
	title string,
) (renderResult, error) {
	tables, err := api.listNotableTables(ctx, baseID)
	if err != nil {
		return renderResult{}, err
	}

	var builder strings.Builder
	if heading := baseHeadingText(title); heading != "" {
		builder.WriteString("# ")
		builder.WriteString(heading)
		builder.WriteString("\n\n")
	}
	for index, table := range tables {
		tableID := strings.TrimSpace(table.ID)
		if tableID == "" {
			continue
		}
		fields, err := api.listNotableFields(ctx, baseID, tableID)
		if err != nil {
			return renderResult{}, err
		}
		if len(fields) == 0 {
			// A table without columns has nothing to render; it cannot hold
			// records either, so it contributes no heading and no empty table.
			continue
		}

		rows, truncated, err := readNotableRecords(ctx, api, baseID, tableID, fields)
		if err != nil {
			// Hand the failure back untouched: the Base is reported as a failed
			// item and retried next sync, so half of its tables are never
			// ingested as if the rest did not exist.
			return renderResult{}, err
		}
		if truncated {
			// Deterministic data loss must be loud: a capped table can never
			// become complete on a later sync, so the extent that was dropped is
			// named rather than left to be discovered.
			logger.Warnf(ctx,
				"[DingTalk] notable table %q in base %s exceeds the %d record ingest cap: "+
					"dropped records %d and beyond; raise maxBaseRecords to sync it in full",
				table.Name, baseID, maxBaseRecords, maxBaseRecords+1)
		}

		name := strings.TrimSpace(table.Name)
		if name == "" {
			name = fmt.Sprintf("Table %d", index+1)
		}
		fmt.Fprintf(&builder, "## %s\n\n", baseHeadingText(name))
		renderTable(&builder, rows)
	}

	markdown := strings.TrimSpace(builder.String())
	if markdown != "" {
		markdown += "\n"
	}
	return renderResult{Markdown: markdown}, nil
}

// readNotableRecords reads every record of one table, page by page, and returns
// the rows ready for renderTable: a header row followed by one row per record.
// It reports whether the row cap stopped the read early.
//
// Column order is the field listing's order. Record payloads key their values
// by field name in a Go map, whose iteration order is random, so only this
// order can be rendered.
func readNotableRecords(
	ctx context.Context,
	api dingTalkAPI,
	baseID, tableID string,
	fields []notableField,
) ([][]string, bool, error) {
	columns := make([]string, len(fields))
	for index, field := range fields {
		columns[index] = field.Name
	}
	rows := make([][]string, 1, maxBaseRecords+1)
	rows[0] = columns

	nextToken := ""
	seenTokens := make(map[string]struct{})
	for page := 0; ; page++ {
		if page >= maxPages {
			return nil, false, fmt.Errorf(
				"DingTalk notable record pagination exceeded %d pages", maxPages)
		}
		recordPage, err := api.listNotableRecords(ctx, baseID, tableID, nextToken)
		if err != nil {
			return nil, false, err
		}

		cappedMidPage := false
		for _, record := range recordPage.Records {
			if len(rows)-1 >= maxBaseRecords {
				cappedMidPage = true
				break
			}
			rows = append(rows, notableRow(fields, record))
		}
		token := strings.TrimSpace(recordPage.NextToken)
		if cappedMidPage || (len(rows)-1 >= maxBaseRecords && recordPage.HasMore) {
			return rows, true, nil
		}
		if !recordPage.HasMore {
			return rows, false, nil
		}
		if token == "" {
			// More pages are claimed but none can be requested: stopping here
			// would drop records silently, so the table fails instead.
			return nil, false, errors.New(
				"DingTalk notable record pagination reported more pages without a nextToken")
		}
		if _, repeated := seenTokens[token]; repeated {
			return nil, false, errors.New("DingTalk notable record pagination repeated nextToken")
		}
		seenTokens[token] = struct{}{}
		nextToken = token
	}
}

// notableRow renders one record in column order. A column the record does not
// carry is an empty cell, not a missing column.
func notableRow(fields []notableField, record notableRecord) []string {
	row := make([]string, len(fields))
	for index, field := range fields {
		row[index] = notableFieldText(field, record.Fields[field.Name])
	}
	return row
}

// notableFieldText renders one cell. The field's type is consulted first
// because a date can only be told apart from a large number by its definition.
func notableFieldText(field notableField, value any) string {
	if strings.EqualFold(strings.TrimSpace(field.Type), "date") {
		if millis, ok := notableEpochMillis(value); ok {
			return time.UnixMilli(millis).In(notableDateLocation).Format("2006-01-02")
		}
	}
	return notableValueText(value)
}

// notableEpochMillis reads a date cell, which the API has delivered as a JSON
// number and as a numeric string.
func notableEpochMillis(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case json.Number:
		millis, err := typed.Int64()
		return millis, err == nil
	case string:
		millis, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return millis, err == nil
	default:
		return 0, false
	}
}

// notableValueText renders one cell as a readable scalar. Notable does not
// return plain strings: a single select arrives as {"name":..,"id":..}, a user
// or multi-select as an array of those, a link as {"linkedRecordIds":[..]}, and
// progress as a decimal string. Printing such a value with Go formatting would
// put "map[name:高 id:...]" in the knowledge base, so each shape is reduced to
// the text a reader would see in DingTalk.
func notableValueText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	case map[string]any:
		for _, key := range []string{"name", "text", "title"} {
			if text, ok := typed[key].(string); ok && strings.TrimSpace(text) != "" {
				return text
			}
		}
		// No display text: a link carries only record ids. Nested values are
		// rendered deterministically so the row never depends on map order.
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			if text := notableValueText(typed[key]); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, ", ")
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := notableValueText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, ", ")
	default:
		// The notable payload is decoded into any, so only the JSON shapes
		// above can occur; anything else has no readable rendering.
		return ""
	}
}

// readBase turns one explicitly referenced Base into a FetchedItem.
//
// A Base is a wiki node, so its display name is read through getNode exactly as
// a node reference's is; the id stays the fallback for a tenant whose wiki API
// does not report one, because an unreadable name must never fail a sync whose
// content is perfectly readable. Naming it matters: an item titled by its id
// is unreadable in the knowledge base.
func readBase(
	ctx context.Context,
	api dingTalkAPI,
	sourceResourceID, baseID string,
) (types.FetchedItem, error) {
	title := baseTitle(ctx, api, baseID)
	rendered, err := renderBase(ctx, api, baseID, title)
	if err != nil {
		return types.FetchedItem{}, err
	}
	return types.FetchedItem{
		ExternalID:  baseID,
		Title:       title,
		Content:     []byte(rendered.Markdown),
		ContentType: "text/markdown",
		// The Base is rendered as markdown, so it is named the way this
		// connector already names a rendered document: the node's own name plus
		// the extension of the body being stored.
		FileName: sanitizeFilename(title) + ".md",
		Metadata: map[string]string{
			"channel":   types.ChannelDingtalk,
			"base_id":   baseID,
			"extension": "able",
			"category":  "ALIDOC",
		},
		SourceResourceID: sourceResourceID,
	}, nil
}

// baseTitle resolves a Base's display name. getNode is the same call the picker
// uses to describe the Base, so the synced title and the name the user selected
// cannot disagree. A read failure is not fatal: the id is still a name, and the
// Base's tables are what the sync is for.
func baseTitle(ctx context.Context, api dingTalkAPI, baseID string) string {
	described, err := api.getNode(ctx, baseID)
	if err != nil {
		logger.Debugf(ctx,
			"[DingTalk] base %s has no readable name (%v); titling it by id", baseID, err)
		return node{ID: baseID}.title()
	}
	// node.title() is the connector's one rule for a displayed node name: the
	// reported name when there is one, the id otherwise.
	return node{ID: baseID, Name: described.Name}.title()
}
