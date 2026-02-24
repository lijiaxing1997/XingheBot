//go:build !windows

package supervisor

import (
	"os"
	"syscall"
)

func supervisorSignals() []os.Signal {
	return []os.Signal{
		os.Interrupt,
		syscall.SIGTERM,
		syscall.SIGHUP,
		syscall.SIGQUIT,
	}
}
