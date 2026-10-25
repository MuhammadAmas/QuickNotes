package main

import (
	"context"
	"fmt"
	"os"

	"github.com/MuhammadAmas/QuickNotes/mcp/internal/auth"
)

// login runs `quicknotes-mcp login`: browser sign-in saved to the server's own session file.
// The Mac app's session.json is never touched.
func login(ctx context.Context, cfg auth.Config, sessionPath string) error {
	authOpt, _, err := testBackend(os.Getenv("QUICKNOTES_TEST_BACKEND"))
	if err != nil {
		return err
	}
	user, err := auth.Login(ctx, cfg, sessionPath, auth.LoginOptions{
		Tokens: authOpt,
		Notify: func(msg string) { fmt.Fprintln(os.Stderr, msg) },
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Signed in as %s. quicknotes-mcp will use this session when the Mac app isn't signed in.\n", user.Email)
	return nil
}
