package restart

import (
	"os"
	"os/exec"
)

type SpawnResult struct {
	PID        int      `json:"pid"`
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	Dir        string   `json:"dir"`
}

func SpawnReplacement(executable string, args []string) (SpawnResult, error) {
	exe := executable
	if exe == "" {
		resolved, err := os.Executable()
		if err != nil {
			return SpawnResult{}, err
		}
		exe = resolved
	}
	cwd, _ := os.Getwd()
	cmd := exec.Command(exe, args...)
	cmd.Env = os.Environ()
	cmd.Dir = cwd
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return SpawnResult{}, err
	}
	return SpawnResult{
		PID:        cmd.Process.Pid,
		Executable: exe,
		Args:       args,
		Dir:        cwd,
	}, nil
}
