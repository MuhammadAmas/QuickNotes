//go:build !darwin

package auth

import "errors"

// openOnMac is unavailable off macOS; Login then prints the address to open by hand.
func openOnMac(string) error { return errors.New("no browser opener on this system") }
