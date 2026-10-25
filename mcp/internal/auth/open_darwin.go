package auth

import "os/exec"

// openOnMac opens url in the default browser.
func openOnMac(url string) error { return exec.Command("open", url).Start() }
