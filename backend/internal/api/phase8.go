package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/downloads"
	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
)

type phase8Handler struct {
	auth      *auth.Service
	hf        *huggingface.Client
	secrets   *huggingface.SecretStore
	downloads *downloads.Manager
}

func NewPhase8Handler(a *auth.Service, hf *huggingface.Client, secrets *huggingface.SecretStore, downloadManager *downloads.Manager) http.Handler {
	return &phase8Handler{auth: a, hf: hf, secrets: secrets, downloads: downloadManager}
}

func (h *phase8Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !phase7RequireUser(h.auth, w, r) {
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
	case path == "/api/v1/downloads":
		h.downloadCollection(w, r)
	case strings.HasPrefix(path, "/api/v1/downloads/"):
		h.downloadItem(w, r, strings.TrimPrefix(path, "/api/v1/downloads/"))
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *phase8Handler) search(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.hf.Search(r.Context(), huggingface.SearchOptions{
		Query: r.URL.Query().Get("q"), Author: r.URL.Query().Get("author"),
		Sort: r.URL.Query().Get("sort"), Limit: limit,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *phase8Handler) detail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	repoID := strings.TrimSpace(r.URL.Query().Get("repo"))
	if repoID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo is required"})
		return
	}
	item, err := h.hf.Detail(r.Context(), repoID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *phase8Handler) token(w http.ResponseWriter, r *http.Request) {
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

func (h *phase8Handler) downloadCollection(w http.ResponseWriter, r *http.Request) {
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

func (h *phase8Handler) downloadItem(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) == 1 && parts[0] != "" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
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
