//go:build linux

package privileges

import (
	"fmt"
	"os"
	"syscall"
)

const nonRootID = 65532

// Drop permanently switches a root process to Mailflow's unprivileged runtime identity.
func Drop() error {
	if os.Geteuid() != 0 {
		return nil
	}
	if err := syscall.Setgroups([]int{}); err != nil {
		return fmt.Errorf("clear supplementary groups: %w", err)
	}
	if err := syscall.Setgid(nonRootID); err != nil {
		return fmt.Errorf("set group ID: %w", err)
	}
	if err := syscall.Setuid(nonRootID); err != nil {
		return fmt.Errorf("set user ID: %w", err)
	}
	return nil
}
