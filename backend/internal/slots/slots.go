package slots

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

var allowedActions = map[string]bool{
	"save":    true,
	"restore": true,
	"erase":   true,
}

// UpstreamPath maps a gateway slots route to the native llama.cpp worker path.
func UpstreamPath(method, slotID string) string {
	if strings.EqualFold(method, http.MethodPost) && strings.TrimSpace(slotID) != "" {
		return "/slots/" + strings.TrimSpace(slotID)
	}
	return "/slots"
}

// SanitizeQuery drops manager-only query parameters before proxying.
func SanitizeQuery(raw url.Values) url.Values {
	if len(raw) == 0 {
		return url.Values{}
	}
	out := make(url.Values, len(raw))
	for key, values := range raw {
		if strings.EqualFold(key, "model") {
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	return out
}

// ValidateAction ensures POST slot actions are allowlisted.
func ValidateAction(action string) error {
	action = strings.TrimSpace(action)
	if action == "" {
		return errors.New("action query parameter is required")
	}
	if !allowedActions[action] {
		return fmt.Errorf("unsupported slots action %q", action)
	}
	return nil
}

// ValidateFilename rejects save/restore filenames that could escape the manager-owned directory.
func ValidateFilename(filename string) error {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return errors.New("filename is required")
	}
	if strings.Contains(filename, "..") || strings.ContainsAny(filename, `/\`) {
		return errors.New("filename must not contain path separators or parent-directory segments")
	}
	return nil
}

type filenameBody struct {
	Filename string `json:"filename"`
}

// ValidateRequestBody validates save/restore JSON bodies and returns the body bytes to forward.
func ValidateRequestBody(body []byte, action string) ([]byte, error) {
	action = strings.TrimSpace(action)
	if action == "erase" {
		return body, nil
	}
	if action != "save" && action != "restore" {
		return body, nil
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, errors.New("filename is required")
	}
	var payload filenameBody
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("invalid request body")
	}
	if err := ValidateFilename(payload.Filename); err != nil {
		return nil, err
	}
	return body, nil
}

// Proxy forwards a slots request to a READY worker endpoint.
func Proxy(w http.ResponseWriter, r *http.Request, workerEndpoint, upstreamPath string, body []byte, mapNotImplemented bool) error {
	target, err := url.Parse(workerEndpoint)
	if err != nil {
		return err
	}
	r = r.Clone(r.Context())
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Del("Authorization")
	r.URL.Path = upstreamPath
	r.URL.RawQuery = SanitizeQuery(r.URL.Query()).Encode()

	proxy := httputil.NewSingleHostReverseProxy(target)
	original := proxy.Director
	proxy.Director = func(req *http.Request) {
		original(req)
		req.Host = target.Host
		req.URL.Path = upstreamPath
		req.URL.RawQuery = SanitizeQuery(req.URL.Query()).Encode()
		req.Header.Del("Authorization")
	}
	if mapNotImplemented {
		proxy.ModifyResponse = func(resp *http.Response) error {
			if resp.StatusCode != http.StatusNotFound {
				return nil
			}
			payload, _ := json.Marshal(map[string]any{"error": map[string]any{
				"message": "This llama.cpp worker does not implement this route",
				"type":    "invalid_request_error",
				"param":   nil,
				"code":    "not_implemented",
			}})
			resp.StatusCode = http.StatusNotImplemented
			resp.Body = io.NopCloser(bytes.NewReader(payload))
			resp.ContentLength = int64(len(payload))
			resp.Header.Set("Content-Length", fmt.Sprint(len(payload)))
			resp.Header.Set("Content-Type", "application/json")
			return nil
		}
	}
	proxy.ServeHTTP(w, r)
	return nil
}
