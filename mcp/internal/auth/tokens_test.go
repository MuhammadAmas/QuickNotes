package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	canaryRefresh = "CANARY-REFRESH-7f3a"
	canaryID      = "CANARY-IDTOKEN-91bc"
)

// fakeIDToken builds a JWT-shaped token whose payload carries the given claims.
func fakeIDToken(t *testing.T, email, name string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"sub": "uid-1", "email": email, "name": name})
	return "hdr." + base64.RawURLEncoding.EncodeToString(payload) + "." + canaryID
}

// fakeGoogle answers securetoken requests: refresh tokens in good get an ID token, others get 400.
func fakeGoogle(t *testing.T, good map[string]string, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("key") != "test-key" || r.FormValue("grant_type") != "refresh_token" {
			http.Error(w, "bad request shape", http.StatusTeapot)
			return
		}
		email, ok := good[r.FormValue("refresh_token")]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"INVALID_REFRESH_TOKEN"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id_token": fakeIDToken(t, email, "Owner"), "expires_in": "3600", "user_id": "uid-1", "refresh_token": r.FormValue("refresh_token"),
		})
	}))
}

func writeSession(t *testing.T, dir, name, refresh string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	b, _ := json.Marshal(map[string]string{"refreshToken": refresh, "uid": "uid-1", "email": "owner@example.com", "name": "Owner"})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) // fixed mtime so any rewrite is visible
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

type fileState struct {
	bytes []byte
	mode  os.FileMode
	mtime time.Time
}

func stateOf(t *testing.T, path string) fileState {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("session file missing: %v", err)
	}
	b, _ := os.ReadFile(path)
	return fileState{b, info.Mode().Perm(), info.ModTime()}
}

func assertUntouched(t *testing.T, path string, before fileState) {
	t.Helper()
	after := stateOf(t, path)
	if !bytes.Equal(before.bytes, after.bytes) || before.mode != after.mode || !before.mtime.Equal(after.mtime) {
		t.Errorf("session file changed: mode %v→%v mtime %v→%v", before.mode, after.mode, before.mtime, after.mtime)
	}
}

func TestTokenUsesMacSessionAndCaches(t *testing.T) {
	var calls atomic.Int32
	srv := fakeGoogle(t, map[string]string{canaryRefresh: "Owner@Example.com"}, &calls)
	defer srv.Close()
	dir := t.TempDir()
	mac := writeSession(t, dir, "session.json", canaryRefresh)
	before := stateOf(t, mac)

	ts := NewTokenSource(Config{APIKey: "test-key"}, []SessionFile{{Path: mac, Label: "Mac app session"}}, Options{Endpoint: srv.URL})
	for range 3 {
		id, user, err := ts.Token(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(id, canaryID) || user.UID != "uid-1" || user.Email != "owner@example.com" || user.Name != "Owner" {
			t.Fatalf("got user %+v", user)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("ID token should be cached; securetoken called %d times", calls.Load())
	}
	assertUntouched(t, mac, before)
}

func TestTokenRefreshesNearExpiry(t *testing.T) {
	var calls atomic.Int32
	srv := fakeGoogle(t, map[string]string{canaryRefresh: "owner@example.com"}, &calls)
	defer srv.Close()
	mac := writeSession(t, t.TempDir(), "session.json", canaryRefresh)
	clock := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	ts := NewTokenSource(Config{APIKey: "test-key"}, []SessionFile{{Path: mac}}, Options{Endpoint: srv.URL, Now: func() time.Time { return clock }})
	if _, _, err := ts.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(59*time.Minute + 30*time.Second) // inside the last minute
	if _, _, err := ts.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected a refresh near expiry, got %d calls", calls.Load())
	}
}

func TestRejectedMacSessionFallsBackAndStaysUntouched(t *testing.T) {
	var calls atomic.Int32
	srv := fakeGoogle(t, map[string]string{"GOOD-LOGIN-TOKEN": "owner@example.com"}, &calls)
	defer srv.Close()
	dir := t.TempDir()
	mac := writeSession(t, dir, "session.json", canaryRefresh) // rejected by the fake
	login := writeSession(t, dir, "mcp-session.json", "GOOD-LOGIN-TOKEN")
	before := stateOf(t, mac)

	ts := NewTokenSource(Config{APIKey: "test-key"}, []SessionFile{{Path: mac, Label: "Mac app session"}, {Path: login, Label: "quicknotes-mcp login"}}, Options{Endpoint: srv.URL})
	if _, _, err := ts.Token(context.Background()); err != nil {
		t.Fatalf("should fall back to the login session: %v", err)
	}
	assertUntouched(t, mac, before)
}

func TestNoUsableSessionSaysHowToSignIn(t *testing.T) {
	var calls atomic.Int32
	srv := fakeGoogle(t, map[string]string{}, &calls)
	defer srv.Close()
	dir := t.TempDir()
	mac := writeSession(t, dir, "session.json", canaryRefresh)
	before := stateOf(t, mac)

	for name, sessions := range map[string][]SessionFile{
		"missing files":    {{Path: filepath.Join(dir, "nope.json")}},
		"rejected session": {{Path: mac, Label: "Mac app session"}},
	} {
		ts := NewTokenSource(Config{APIKey: "test-key"}, sessions, Options{Endpoint: srv.URL})
		_, _, err := ts.Token(context.Background())
		if !errors.Is(err, ErrNotSignedIn) {
			t.Errorf("%s: want ErrNotSignedIn, got %v", name, err)
			continue
		}
		if !strings.Contains(err.Error(), "quicknotes-mcp login") || strings.Contains(err.Error(), canaryRefresh) {
			t.Errorf("%s: unhelpful or leaking message: %v", name, err)
		}
	}
	assertUntouched(t, mac, before)
}

func TestNetworkErrorsDoNotLeakSecrets(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(300 * time.Millisecond) }))
	defer slow.Close()
	mac := writeSession(t, t.TempDir(), "session.json", canaryRefresh)
	cases := map[string]Options{
		"timeout":     {Endpoint: slow.URL, HTTPClient: &http.Client{Timeout: 50 * time.Millisecond}},
		"unreachable": {Endpoint: "http://127.0.0.1:1"},
	}
	for name, opt := range cases {
		ts := NewTokenSource(Config{APIKey: "SECRET-API-KEY"}, []SessionFile{{Path: mac}}, opt)
		_, _, err := ts.Token(context.Background())
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		for _, secret := range []string{canaryRefresh, "SECRET-API-KEY", "127.0.0.1"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: error %q leaks %q", name, err, secret)
			}
		}
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "quicknotes.config.json")
	_ = os.WriteFile(cfgPath, []byte(`{"firebase":{"apiKey":"k","projectId":"p"},"webURL":"https://w.example"}`), 0o600)
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	cfg, err := LoadConfig(env(map[string]string{"QUICKNOTES_CONFIG": cfgPath}), "")
	if err != nil || cfg != (Config{APIKey: "k", ProjectID: "p", WebURL: "https://w.example"}) {
		t.Errorf("from file: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig(env(map[string]string{"QUICKNOTES_API_KEY": "ek", "QUICKNOTES_PROJECT_ID": "ep"}), "")
	if err != nil || cfg.APIKey != "ek" || cfg.ProjectID != "ep" {
		t.Errorf("from env: %+v %v", cfg, err)
	}
	// binary at <repo>/mcp/bin/quicknotes-mcp finds <repo>/quicknotes.config.json
	exe := filepath.Join(dir, "mcp", "bin", "quicknotes-mcp")
	if cfg, err = LoadConfig(env(nil), exe); err != nil || cfg.ProjectID != "p" {
		t.Errorf("next to binary: %+v %v", cfg, err)
	}
	if _, err = LoadConfig(env(map[string]string{"QUICKNOTES_CONFIG": filepath.Join(dir, "missing.json")}), ""); err == nil {
		t.Error("missing file should fail")
	}
}
