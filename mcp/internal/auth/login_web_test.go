package auth

import (
	"os/exec"
	"testing"
)

// TestLinkPageLoopbackRules runs the web-side half of the sign-in safety Oracle
// (web/link-return.js under Node), so `go test ./...` covers both halves.
func TestLinkPageLoopbackRules(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("blocked: node is not installed, so web/link-return.js cannot be checked")
	}
	cmd := exec.Command(node, "--test", "link-return.test.mjs")
	cmd.Dir = "../../oracle-tests/scenarios/loopback-signin-safety"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("web sign-in rules failed: %v\n%s", err, out)
	}
}
