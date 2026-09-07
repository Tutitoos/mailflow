//go:build !linux

package privileges

// Drop is a no-op on platforms without the Linux container privilege model.
func Drop() error {
	return nil
}
