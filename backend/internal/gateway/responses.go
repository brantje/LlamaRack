package gateway

import (
	"bytes"
	"encoding/json"
	"strings"
)

func extractJSONID(payload []byte) string {
	var value map[string]any
	if json.Unmarshal(payload, &value) != nil {
		return ""
	}
	return firstNonEmpty(
		stringValue(value["id"]),
		nestedString(value, "response", "id"),
	)
}

func extractOpenAIResponseObject(payload []byte) (map[string]any, bool) {
	var value map[string]any
	if json.Unmarshal(bytes.TrimSpace(payload), &value) != nil {
		return nil, false
	}
	if nested, ok := value["response"].(map[string]any); ok {
		if stringValue(nested["object"]) == "response" || stringValue(nested["id"]) != "" {
			return nested, true
		}
	}
	if stringValue(value["object"]) == "response" || strings.HasPrefix(stringValue(value["id"]), "resp_") {
		return value, true
	}
	return nil, false
}

func parseResponseIDFromSSE(body []byte) string {
	id := ""
	walkSSE(body, func(event string, data []byte) {
		if candidate := extractJSONID(data); candidate != "" {
			id = candidate
		}
		if obj, ok := extractOpenAIResponseObject(data); ok {
			if candidate := stringValue(obj["id"]); candidate != "" {
				id = candidate
			}
		}
		_ = event
	})
	if id != "" {
		return id
	}
	return extractJSONID(body)
}

func parseFinalResponseJSON(body []byte) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if obj, ok := extractOpenAIResponseObject(trimmed); ok {
			payload, err := json.Marshal(obj)
			return payload, err == nil
		}
		return nil, false
	}
	var latest json.RawMessage
	walkSSE(body, func(event string, data []byte) {
		obj, ok := extractOpenAIResponseObject(data)
		if !ok {
			return
		}
		if event == "response.completed" || stringValue(obj["status"]) == "completed" || latest == nil {
			if payload, err := json.Marshal(obj); err == nil {
				latest = payload
			}
		}
	})
	if len(latest) == 0 {
		return nil, false
	}
	return latest, true
}

func walkSSE(body []byte, fn func(event string, data []byte)) {
	event := ""
	var data bytes.Buffer
	flush := func() {
		if data.Len() == 0 {
			event = ""
			return
		}
		fn(event, append([]byte(nil), data.Bytes()...))
		event = ""
		data.Reset()
	}
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		if len(bytes.TrimSpace(line)) == 0 {
			flush()
			continue
		}
		if bytes.HasPrefix(line, []byte("event:")) {
			event = string(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("event:"))))
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if bytes.Equal(payload, []byte("[DONE]")) {
				continue
			}
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.Write(payload)
		}
	}
	flush()
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func nestedString(value map[string]any, keys ...string) string {
	current := any(value)
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	return stringValue(current)
}

func extractQuotedID(buf []byte) string {
	key := []byte(`"id":"`)
	index := bytes.Index(buf, key)
	if index < 0 {
		return ""
	}
	rest := buf[index+len(key):]
	end := bytes.IndexByte(rest, '"')
	if end <= 0 {
		return ""
	}
	return string(rest[:end])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
