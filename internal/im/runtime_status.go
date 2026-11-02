package im

// RuntimeStatus reports the current process IM queue, independently of Asynq.
type RuntimeStatus struct {
	Available  bool  `json:"available"`
	Workers    int   `json:"workers"`
	QueueLimit int   `json:"queue_limit"`
	Pending    int   `json:"pending"`
	Active     int64 `json:"active"`
	Rejected   int64 `json:"rejected"`
	TimedOut   int64 `json:"timed_out"`
}

// RuntimeStatus keeps disabled queues unavailable rather than reporting misleading zero capacity.
func (s *Service) RuntimeStatus() RuntimeStatus {
	if s == nil || s.qaQueue == nil {
		return RuntimeStatus{}
	}
	m := s.qaQueue.Metrics()
	return RuntimeStatus{
		Available:  true,
		Workers:    s.qaQueue.workers,
		QueueLimit: s.qaQueue.maxSize,
		Pending:    m.Depth,
		Active:     m.ActiveWorkers,
		Rejected:   m.TotalRejected,
		TimedOut:   m.TotalTimeout,
	}
}
