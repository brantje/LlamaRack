package gateway

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	defaultInputItemLimit = 20
	maxInputItemLimit     = 100
)

func normalizeInputItems(requestBody []byte) []map[string]any {
	var envelope map[string]any
	if json.Unmarshal(requestBody, &envelope) != nil {
		return nil
	}
	raw, ok := envelope["input"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case string:
		return []map[string]any{inputMessageItem(0, typed)}
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for index, item := range typed {
			switch value := item.(type) {
			case string:
				out = append(out, inputMessageItem(index, value))
			case map[string]any:
				if stringValue(value["id"]) == "" {
					value["id"] = inputItemID(index)
				}
				if stringValue(value["type"]) == "" {
					value["type"] = "message"
				}
				out = append(out, value)
			}
		}
		return out
	default:
		return nil
	}
}

func inputMessageItem(index int, text string) map[string]any {
	return map[string]any{
		"id":   inputItemID(index),
		"type": "message",
		"role": "user",
		"content": []map[string]any{{
			"type": "input_text",
			"text": text,
		}},
	}
}

func inputItemID(index int) string {
	return "msg_" + strconv.Itoa(index)
}

func paginateInputItems(items []map[string]any, after string, limit int) (data []map[string]any, hasMore bool) {
	if limit <= 0 {
		limit = defaultInputItemLimit
	}
	if limit > maxInputItemLimit {
		limit = maxInputItemLimit
	}
	start := 0
	if after != "" {
		start = len(items)
		for i, item := range items {
			if stringValue(item["id"]) == after {
				start = i + 1
				break
			}
		}
	}
	if start >= len(items) {
		return []map[string]any{}, false
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], end < len(items)
}

func inputItemsList(items []map[string]any, after string, limit int) map[string]any {
	data, hasMore := paginateInputItems(items, after, limit)
	firstID, lastID := "", ""
	if len(data) > 0 {
		firstID = stringValue(data[0]["id"])
		lastID = stringValue(data[len(data)-1]["id"])
	}
	return map[string]any{
		"object":   "list",
		"data":     data,
		"first_id": firstID,
		"last_id":  lastID,
		"has_more": hasMore,
	}
}

func parseLimitQuery(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultInputItemLimit
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return defaultInputItemLimit
	}
	return value
}
