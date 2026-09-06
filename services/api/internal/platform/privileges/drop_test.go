package privileges

import "testing"

func TestDropIsSafeForUnprivilegedProcess(t *testing.T) {
	if err := Drop(); err != nil {
		t.Fatal(err)
	}
}
