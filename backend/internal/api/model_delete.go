package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/brantje/llamacpp-manager/backend/internal/models"
)

func (s *Server) deleteModel(w http.ResponseWriter, r *http.Request, id string) {
	deleteFiles := false
	if raw := r.URL.Query().Get("delete_files"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "delete_files must be true or false"})
			return
		}
		deleteFiles = value
	}

	var plan models.FileDeletePlan
	var err error
	if deleteFiles {
		// Resolve and validate every persisted artifact target before stopping
		// Instances. The Model service revalidates the plan before unlinking.
		plan, err = s.models.PrepareFileDeletion(r.Context(), id)
		if err != nil {
			writeModelDeleteError(w, err)
			return
		}
	}

	if err := s.lifecycle.StopModel(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if deleteFiles {
		err = s.models.DeleteFilesAndModel(r.Context(), id, plan)
	} else {
		err = s.models.Delete(r.Context(), id)
	}
	if err != nil {
		writeModelDeleteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeModelDeleteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "model not found"})
	case errors.Is(err, models.ErrArtifactShared):
		writeErr(w, http.StatusConflict, err)
	case errors.Is(err, models.ErrUnsafeArtifactPath):
		writeErr(w, http.StatusBadRequest, err)
	default:
		writeErr(w, http.StatusInternalServerError, err)
	}
}
