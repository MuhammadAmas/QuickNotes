package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LoginOptions tune Login; zero values give production behaviour.
type LoginOptions struct {
	OpenBrowser func(url string) error // default: macOS `open`
	Timeout     time.Duration          // default 5 minutes
	Tokens      Options                // for checking the new session (tests point this at a fake)
	Notify      func(msg string)       // progress messages for the terminal (never contain tokens)
}

// Login signs in through the browser for when the Mac app isn't signed in:
//
//  1. listen on 127.0.0.1 (a random free port) for one POST to /callback;
//  2. open <WebURL>/link?return=http://127.0.0.1:<port>/callback&state=<random>;
//  3. after Google sign-in, the page asks the user to confirm, then POSTs the refresh token and
//     state in a form body (never in a URL, so it can't end up in browser history);
//  4. check the state, check the token with Google, and save it to sessionPath (mode 0600).
//
// The listener stops after the first callback (right or wrong), or after the timeout.
func Login(ctx context.Context, cfg Config, sessionPath string, opt LoginOptions) (User, error) {
	if cfg.WebURL == "" {
		return User{}, errors.New("the QuickNotes web address is unknown: set webURL in quicknotes.config.json or QUICKNOTES_WEB_URL")
	}
	if opt.Timeout == 0 {
		opt.Timeout = 5 * time.Minute
	}
	if opt.Notify == nil {
		opt.Notify = func(string) {}
	}
	if opt.OpenBrowser == nil {
		opt.OpenBrowser = openOnMac
	}
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		return User{}, errors.New("could not create a sign-in code")
	}
	state := hex.EncodeToString(stateBytes)

	ln, err := net.Listen("tcp", "127.0.0.1:0") // loopback only: nothing outside this Mac can connect
	if err != nil {
		return User{}, errors.New("could not open a local port for the sign-in callback")
	}
	port := ln.Addr().(*net.TCPAddr).Port
	host := fmt.Sprintf("127.0.0.1:%d", port)
	callback := "http://" + host + "/callback"

	type result struct {
		refresh string
		err     error
	}
	done := make(chan result, 1)
	finish := func(r result) {
		select {
		case done <- r:
		default:
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host { // blocks DNS-rebinding pages that reach us under another name
			http.Error(w, "wrong host", http.StatusMisdirectedRequest)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "QuickNotes sign-in callback: expected a form POST", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			finish(result{err: errors.New("the sign-in callback was malformed")})
			return
		}
		got := r.PostForm.Get("state")
		if subtle.ConstantTimeCompare([]byte(got), []byte(state)) != 1 {
			writePage(w, http.StatusForbidden, "Sign-in not accepted", "This sign-in did not come from the quicknotes-mcp login you started. Nothing was saved.")
			finish(result{err: errors.New("the sign-in callback had the wrong code; nothing was saved (run `quicknotes-mcp login` again)")})
			return
		}
		refresh := r.PostForm.Get("refresh_token")
		if refresh == "" {
			writePage(w, http.StatusBadRequest, "Sign-in not accepted", "No session was received. Nothing was saved.")
			finish(result{err: errors.New("the sign-in callback carried no session")})
			return
		}
		writePage(w, http.StatusOK, "QuickNotes is connected", "You can close this tab and go back to your terminal.")
		finish(result{refresh: refresh})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	link := strings.TrimRight(cfg.WebURL, "/") + "/link?" + url.Values{"return": {callback}, "state": {state}}.Encode()
	opt.Notify("Opening your browser to sign in to QuickNotes. If it doesn't open, visit:\n  " + link)
	if err := opt.OpenBrowser(link); err != nil {
		opt.Notify("Could not open the browser automatically; open the address above yourself.")
	}

	var r result
	select {
	case r = <-done:
	case <-time.After(opt.Timeout):
		return User{}, fmt.Errorf("no sign-in within %s; run `quicknotes-mcp login` again", opt.Timeout)
	case <-ctx.Done():
		return User{}, errors.New("sign-in cancelled")
	}
	if r.err != nil {
		return User{}, r.err
	}

	// Check the session with Google before saving it.
	check := NewTokenSource(cfg, nil, opt.Tokens)
	_, user, _, err := check.exchange(ctx, r.refresh)
	if errors.Is(err, errRejected) {
		return User{}, errors.New("Google did not accept the received session; nothing was saved")
	}
	if err != nil {
		return User{}, err
	}
	if err := saveSession(sessionPath, savedSession{RefreshToken: r.refresh, UID: user.UID, Email: user.Email, Name: user.Name}); err != nil {
		return User{}, err
	}
	return user, nil
}

// saveSession writes the login file atomically with owner-only permissions.
func saveSession(path string, s savedSession) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return errors.New("could not create the QuickNotes settings folder")
	}
	data, _ := json.Marshal(s)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcp-session-*")
	if err != nil {
		return errors.New("could not save the session")
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return errors.New("could not protect the session file")
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return errors.New("could not save the session")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("could not save the session")
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.New("could not save the session")
	}
	return nil
}

func writePage(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title>`+
		`<body style="font:16px -apple-system,sans-serif;max-width:32em;margin:15vh auto;padding:0 16px;text-align:center">`+
		`<h1 style="font-size:22px">%s</h1><p>%s</p></body>`, title, title, message)
}
