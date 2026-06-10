//go:build linux || dragonfly || freebsd || netbsd || openbsd || darwin

package asynq

import (
	"os"
	"os/signal"

	"golang.org/x/sys/unix"
)

// waitForSignals waits for signals and handles them.
// It handles SIGTERM, SIGINT, and SIGTSTP.
// SIGTERM and SIGINT will signal the process to exit.
// SIGTSTP will signal the process to stop processing new tasks.
func (srv *Server) waitForSignals() {
	srv.logger.Info("Send signal TSTP to stop processing new tasks")
	srv.logger.Info("Send signal TERM or INT to terminate the process")

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, unix.SIGTERM, unix.SIGINT, unix.SIGTSTP)
	for {
		sig := <-sigs
		if sig == unix.SIGTSTP {
			srv.Stop()
			continue
		} else {
			srv.Stop()
			break
		}
	}
}

func (s *Scheduler) waitForSignals() {
	s.logger.Info("Send signal TERM or INT to stop the scheduler")
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, unix.SIGTERM, unix.SIGINT)
	<-sigs
}
