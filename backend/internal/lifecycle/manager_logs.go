package lifecycle

func (s *Service) AddManagerLog(instanceID, line string) {
	if s == nil || s.sup == nil { return }
	s.sup.AddManagerLog(instanceID, line)
}
