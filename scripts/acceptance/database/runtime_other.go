//go:build !windows

package main

// The database driver and its tests compile on other hosts, but this prepared
// run deliberately uses the original Windows candidate and Windows console
// shutdown. Other platforms require their own native executable/run evidence.
func (s *suite) productRuntime() error {
	return errAssertion
}
