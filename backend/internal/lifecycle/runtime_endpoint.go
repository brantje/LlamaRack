package lifecycle

func (s *Service) RuntimeEndpoint(instanceID string) (string, bool) {
	return s.sup.Endpoint(instanceID)
}
