package api

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/brantje/llamarack/backend/internal/models"
)

type modelCreateHandler struct {
	next   http.Handler
	models *models.Service
}

func NewModelCreateHandler(next http.Handler, modelService *models.Service) http.Handler {
	return &modelCreateHandler{next: next, models: modelService}
}

func (h *modelCreateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	capture := &bufferedHTTPResponse{header: make(http.Header)}
	h.next.ServeHTTP(capture, r)
	body := capture.body.Bytes()
	if refreshed := h.refreshModelResponse(r, body); refreshed != nil {
		body = refreshed
	}
	for key, values := range capture.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Del("Content-Length")
	status := capture.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (h *modelCreateHandler) refreshModelResponse(r *http.Request, body []byte) []byte {
	if h.models == nil || len(body) == 0 {
		return nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	rawModel, ok := payload["model"]
	if !ok {
		return nil
	}
	var model models.Model
	if err := json.Unmarshal(rawModel, &model); err != nil || model.ID == "" {
		return nil
	}
	refreshed, err := h.models.RefreshLogicalSize(r.Context(), model.ID)
	if err != nil {
		return nil
	}
	replacement, err := json.Marshal(refreshed)
	if err != nil {
		return nil
	}
	payload["model"] = replacement
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return append(encoded, '\n')
}

type bufferedHTTPResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (r *bufferedHTTPResponse) Header() http.Header { return r.header }

func (r *bufferedHTTPResponse) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *bufferedHTTPResponse) Write(payload []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(payload)
}
