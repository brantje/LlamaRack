package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/downloads"
	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
	"github.com/brantje/llamacpp-manager/backend/internal/modelimports"
)

type huggingFaceHandler struct {
	auth      *auth.Service
	hf        *huggingface.Client
	secrets   *huggingface.SecretStore
	downloads *downloads.Manager
	imports   *modelimports.Service
}

type downloadSnapshotEvent struct {
	Type      string          `json:"type"`
	Downloads []downloads.Job `json:"downloads"`
}

func NewHuggingFaceHandler(a *auth.Service, hf *huggingface.Client, secrets *huggingface.SecretStore, downloadManager *downloads.Manager, importServices ...*modelimports.Service) http.Handler {
	var importService *modelimports.Service
	if len(importServices) > 0 {
		importService = importServices[0]
	}
	return &huggingFaceHandler{auth: a, hf: hf, secrets: secrets, downloads: downloadManager, imports: importService}
}

func (h *huggingFaceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !requireAuthenticatedUser(h.auth, w, r) {
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/v1/huggingface/search":
		h.search(w, r)
	case path == "/api/v1/huggingface/model":
		h.detail(w, r)
	case path == "/api/v1/huggingface/token":
		h.token(w, r)
	case path == "/api/v1/huggingface/import":
		h.prepareImport(w, r)
	case path == "/api/v1/imports":
		h.importStatuses(w, r)
	case path == "/api/v1/downloads/ws":
		h.downloadEvents(w, r)
	case path == "/api/v1/downloads":
		h.downloadCollection(w, r)
	case strings.HasPrefix(path, "/api/v1/downloads/"):
		h.downloadItem(w, r, strings.TrimPrefix(path, "/api/v1/downloads/"))
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *huggingFaceHandler) search(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := h.hf.SearchSortedPage(r.Context(), huggingface.SearchOptions{
		Query: r.URL.Query().Get("q"), Author: r.URL.Query().Get("author"),
		Sort: r.URL.Query().Get("sort"), Limit: limit,
	}, r.URL.Query().Get("cursor"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *huggingFaceHandler) detail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	repoID := strings.TrimSpace(r.URL.Query().Get("repo"))
	if repoID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo is required"})
		return
	}
	item, err := h.hf.DetailWithCard(r.Context(), repoID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *huggingFaceHandler) token(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		status, err := h.secrets.TokenStatus(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
	case http.MethodPut:
		var in struct {
			Token string `json:"token"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := h.secrets.SetToken(r.Context(), in.Token); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		status, err := h.secrets.TokenStatus(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
	case http.MethodDelete:
		if err := h.secrets.DeleteToken(r.Context()); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *huggingFaceHandler) prepareImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.imports == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "provider import service is unavailable"})
		return
	}
	var in struct {
		RepoID        string                          `json:"repo_id"`
		ArtifactID    string                          `json:"artifact_id"`
		Name          string                          `json:"name"`
		ContextLength int                             `json:"context_length"`
		Options       map[string]string               `json:"options"`
		FirstInstance modelimports.FirstInstanceInput `json:"first_instance"`
	}
	if !decode(w, r, &in) {
		return
	}
	detail, err := h.hf.Detail(r.Context(), strings.TrimSpace(in.RepoID))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	var artifact *huggingface.Artifact
	for index := range detail.Artifacts {
		if detail.Artifacts[index].ID == in.ArtifactID {
			artifact = &detail.Artifacts[index]
			break
		}
	}
	if artifact == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "artifact is not part of the current repository revision"})
		return
	}
	result, err := h.imports.Prepare(r.Context(), detail, *artifact, modelimports.PrepareInput{
		Name: in.Name, ContextLength: in.ContextLength, Options: in.Options, FirstInstance: in.FirstInstance,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := h.imports.RepairArtifactOptions(r.Context(), result.Model.ID, detail.ID, *artifact); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (h *huggingFaceHandler) importStatuses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.imports == nil {
		writeJSON(w, http.StatusOK, []modelimports.Status{})
		return
	}
	items, err := h.imports.ListResolved(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *huggingFaceHandler) downloadEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	snapshot, events, cancel, err := h.downloads.Subscribe(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer cancel()

	upgrader := websocket.Upgrader{CheckOrigin: func(request *http.Request) bool {
		return websocketOriginAllowed(request, "")
	}}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	if err := conn.WriteJSON(downloadSnapshotEvent{Type: "download_snapshot", Downloads: snapshot}); err != nil {
		return
	}

	disconnected := make(chan struct{})
	go func() {
		defer close(disconnected)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-disconnected:
			return
		case event, open := <-events:
			if !open {
				return
			}
			if err := conn.WriteJSON(event); err != nil {
				return
			}
		}
	}
}

func (h *huggingFaceHandler) downloadCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.downloads.List(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodPost:
		var in struct {
			RepoID     string `json:"repo_id"`
			ArtifactID string `json:"artifact_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		detail, err := h.hf.Detail(r.Context(), strings.TrimSpace(in.RepoID))
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		var artifact *huggingface.Artifact
		for index := range detail.Artifacts {
			if detail.Artifacts[index].ID == in.ArtifactID {
				artifact = &detail.Artifacts[index]
				break
			}
		}
		if artifact == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "artifact is not part of the current repository revision"})
			return
		}
		job, err := h.downloads.CreateHuggingFace(r.Context(), detail, *artifact)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, job)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *huggingFaceHandler) downloadItem(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) == 1 && parts[0] != "" {
		switch r.Method {
		case http.MethodGet:
			job, err := h.downloads.Get(r.Context(), parts[0])
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": "download not found"})
					return
				}
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			writeJSON(w, http.StatusOK, job)
		case http.MethodDelete:
			job, err := h.downloads.Get(r.Context(), parts[0])
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": "download not found"})
					return
				}
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			if job.State == downloads.StateCancelled && h.imports != nil {
				if err := h.imports.CleanupJobSafe(r.Context(), parts[0]); err != nil {
					writeErr(w, http.StatusInternalServerError, err)
					return
				}
			}
			if err := h.downloads.Remove(r.Context(), parts[0]); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": "download not found"})
					return
				}
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) != 2 || parts[0] == "" || r.Method != http.MethodPost {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	switch parts[1] {
	case "cancel":
		if err := h.downloads.Cancel(r.Context(), parts[0]); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "download not found"})
				return
			}
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "retry":
		job, err := h.downloads.Retry(r.Context(), parts[0])
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "download not found"})
				return
			}
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusAccepted, job)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}
