package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	managersecurity "github.com/brantje/llamarack/backend/internal/security"
)

func suppliedTraceID(r *http.Request, bodyTraceID string) (string, bool) {
	for _, value := range []string{r.Header.Get(headerTraceID), bodyTraceID} {
		if traceID, ok := normalizeUUID(value); ok { return traceID, true }
	}
	return "", false
}

func resolveTraceID(r *http.Request, bodyTraceID string) string {
	if traceID, ok := suppliedTraceID(r, bodyTraceID); ok { return traceID }
	return newTraceID()
}

func normalizeUUID(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' { return "", false }
	for index, r := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 { continue }
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) { return "", false }
	}
	return value, true
}

func newTraceID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		fallback := fmt.Sprintf("%032x", uint64(time.Now().UnixNano())^requestIDFallback.Add(1))
		return fmt.Sprintf("%s-%s-%s-%s-%s", fallback[0:8], fallback[8:12], fallback[12:16], fallback[16:20], fallback[20:32])
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(value[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexValue[0:8], hexValue[8:12], hexValue[12:16], hexValue[16:20], hexValue[20:32])
}

func (g *Gateway) SetNetwork(network *managersecurity.Network) { g.network = network }

func (g *Gateway) clientIP(r *http.Request) string {
	if g.network != nil { return g.network.EffectiveRemoteAddress(r) }
	return canonicalIP(r.RemoteAddr)
}

func canonicalIP(value string) string {
	value = strings.Trim(strings.TrimSpace(value), `"`)
	if value == "" || strings.EqualFold(value, "unknown") || strings.HasPrefix(value, "_") { return "" }
	if host, _, err := net.SplitHostPort(value); err == nil { value = host }
	value = strings.Trim(value, "[]")
	if ip := net.ParseIP(value); ip != nil { return ip.String() }
	return ""
}

func boundedMetadata(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit > 0 && len(value) > limit { value = value[:limit] }
	return value
}
