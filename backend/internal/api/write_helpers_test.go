package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteHelpers(t *testing.T) {
	w := httptest.NewRecorder()
	writeErr(w, 418, nil)
	if w.Code != 418 || !strings.Contains(w.Body.String(), "unknown error") {
		t.Fatalf("writeErr=%d %s", w.Code, w.Body.String())
	}
}
