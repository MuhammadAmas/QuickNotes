// Command quicknotes-mcp is an MCP server for QuickNotes: AI assistants can list, search,
// summarize, add, tick off, move and trash the signed-in user's notes.
//
// It talks to MCP clients over stdio, so stdout is reserved for protocol messages and all
// logging goes to stderr. It signs in with the QuickNotes Mac app's saved session, or with its
// own one from `quicknotes-mcp login`.
//
// Usage:
//
//	quicknotes-mcp          serve MCP over stdio (started by an MCP client)
//	quicknotes-mcp login    sign in through the browser when the Mac app isn't signed in
//
// Settings come from QUICKNOTES_API_KEY / QUICKNOTES_PROJECT_ID / QUICKNOTES_WEB_URL, or from the
// private quicknotes.config.json (QUICKNOTES_CONFIG, or two folders above the binary).
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MuhammadAmas/QuickNotes/mcp/internal/auth"
	"github.com/MuhammadAmas/QuickNotes/mcp/internal/cloud"
	"github.com/MuhammadAmas/QuickNotes/mcp/internal/firestore"
	"github.com/MuhammadAmas/QuickNotes/mcp/internal/tools"
)

const (
	serverName    = "quicknotes-mcp"
	serverVersion = "1.0.0"
)

func main() {
	// stdout carries MCP messages, so all logs must go to stderr.
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	log.SetPrefix("[" + serverName + "] ")

	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("%v", err)
	}
}

// run holds the real work of main, so every exit path is in one place.
func run(args []string) error {
	exe, _ := os.Executable()
	cfg, err := auth.LoadConfig(os.Getenv, exe)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot find your home folder: %w", err)
	}
	sessions := auth.DefaultSessions(home)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(args) > 0 {
		switch args[0] {
		case "login":
			return login(ctx, cfg, sessions[len(sessions)-1].Path)
		case "-h", "--help", "help":
			fmt.Fprintln(os.Stderr, "usage: quicknotes-mcp [login]")
			return nil
		default:
			return fmt.Errorf("unknown command %q; usage: quicknotes-mcp [login]", args[0])
		}
	}

	authOpt, fsOpt, err := testBackend(os.Getenv("QUICKNOTES_TEST_BACKEND"))
	if err != nil {
		return err
	}
	tokens := auth.NewTokenSource(cfg, sessions, authOpt)
	server := newServer(newService(cfg, tokens, fsOpt))
	log.Printf("%s %s starting on stdio (project %s)", serverName, serverVersion, cfg.ProjectID) // never log tokens
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		return err
	}
	log.Println("stopped")
	return nil
}

// testBackend lets tests point the server at a local fake Firebase. Only http://127.0.0.1:<port>
// is accepted, so this setting can never send a token to another machine.
func testBackend(raw string) (auth.Options, firestore.Options, error) {
	if raw == "" {
		return auth.Options{}, firestore.Options{}, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || (u.Path != "" && u.Path != "/") {
		return auth.Options{}, firestore.Options{}, errors.New("QUICKNOTES_TEST_BACKEND must be http://127.0.0.1:<port>")
	}
	base := "http://127.0.0.1:" + u.Port()
	log.Printf("using the local test backend at %s", base)
	return auth.Options{Endpoint: base + "/token"}, firestore.Options{BaseURL: base}, nil
}

// newService connects the notes service to Firestore through the token source.
func newService(cfg auth.Config, tokens *auth.TokenSource, opt firestore.Options) *cloud.Service {
	opt.Forget = tokens.Forget
	fs := firestore.New(cfg.ProjectID, func(ctx context.Context) (string, error) {
		token, _, err := tokens.Token(ctx)
		return token, err
	}, opt)
	return cloud.New(fs, func(ctx context.Context) (auth.User, error) {
		_, user, err := tokens.Token(ctx)
		return user, err
	})
}

// newServer builds the MCP server with every tool, the resource and the prompt.
// It is separate from run so tests can build the same server without stdio.
func newServer(svc *cloud.Service) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Title:   "QuickNotes",
		Version: serverVersion,
	}, &mcp.ServerOptions{
		Instructions: "QuickNotes is the user's day-by-day to-do notes app. Dates are YYYY-MM-DD in the user's time zone. " +
			"Use list_notes for a day or view, search_notes to find notes, and ask before trashing notes.",
	})
	tools.Register(server, svc)
	return server
}
