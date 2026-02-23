//go:build windows

package restart

import "errors"

func ExecReplacement(executable string, args []string) error {
	return errors.New("exec replacement is not supported on windows")
}
