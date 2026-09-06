//go:build !linux

package privileges

func Drop() error {
	return nil
}
