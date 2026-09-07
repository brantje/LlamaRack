package settings

import "context"

type HuggingFace struct {
	MaxDownloadBytes Value `json:"max_download_bytes"`
}

func (s *Service) HuggingFace(ctx context.Context) (HuggingFace, error) {
	value, err := s.Resolve(ctx, MaxDownloadBytes)
	if err != nil {
		return HuggingFace{}, err
	}
	return HuggingFace{MaxDownloadBytes: value}, nil
}
