package im

import "testing"

func TestRuntimeStatusUnavailable(t *testing.T) {
	var service *Service
	if service.RuntimeStatus().Available {
		t.Fatal("nil service must be unavailable")
	}
	if (&Service{}).RuntimeStatus().Available {
		t.Fatal("disabled queue must be unavailable")
	}
}

func TestRuntimeStatusReportsLocalQueue(t *testing.T) {
	q := newQAQueue(5, 50, 3, 0, nil, nil)
	q.queue = []*qaRequest{{}, {}}
	q.activeWorkers.Store(3)
	q.totalRejected.Store(7)
	q.totalTimeout.Store(11)
	status := (&Service{qaQueue: q}).RuntimeStatus()
	if !status.Available || status.Workers != 5 || status.QueueLimit != 50 || status.Pending != 2 ||
		status.Active != 3 || status.Rejected != 7 || status.TimedOut != 11 {
		t.Fatalf("unexpected local queue metrics: %+v", status)
	}
}
