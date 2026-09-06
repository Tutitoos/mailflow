//go:build linux

package privileges

import (
	"os"
	"testing"
)

func TestDropIsSafeForUnprivilegedProcess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires an unprivileged test process")
	}
	if err := Drop(); err != nil {
		t.Fatal(err)
	}
}
