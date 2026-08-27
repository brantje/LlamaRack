package models

import (
	"path/filepath"

	"github.com/brantje/llamacpp-manager/backend/internal/ggufmeta"
)

// ReadGGUFValue returns a bounded page of one metadata value. The normal
// inspection response remains compact while Model details can lazily expand
// large strings and arrays without loading tensor payloads.
func (s *Service) ReadGGUFValue(path, key string, offset, limit uint64) (ggufmeta.ValuePage, error) {
	rel, _, err := s.resolveGGUF(path)
	if err != nil {
		return ggufmeta.ValuePage{}, err
	}
	return ggufmeta.ReadValuePage(filepath.Join(s.modelsDir, filepath.FromSlash(rel)), key, offset, limit)
}
