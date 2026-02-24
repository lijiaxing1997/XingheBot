//go:build windows

package supervisor

import "os"

func supervisorSignals() []os.Signal {
	return []os.Signal{
		os.Interrupt,
	}
}
