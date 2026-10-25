package auth

// Oracle "browser sign-in fallback is safe", Go side.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// browserThat simulates the /link page: it reads the callback and state from the opened address
// and then does whatever post wants with them.
func browserThat(t *testing.T, post func(callback, state string)) func(string) error {
	t.Helper()
	return func(link string) error {
		u, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		if u.Path != "/link" {
			t.Errorf("opened %s, want the /link page", u.Path)
		}
		callback, state := u.Query().Get("return"), u.Query().Get("state")
		cb, _ := url.Parse(callback)
		if cb.Scheme != "http" || cb.Hostname() != "127.0.0.1" || cb.Path != "/callback" || len(state) < 32 {
			t.Errorf("unsafe callback %q / state %q", callback, state)
		}
		go post(callback, state)
		return nil
	}
}

func postForm(t *testing.T, callback string, values url.Values) int {
	t.Helper()
	resp, err := http.PostForm(callback, values)
	if err != nil {
		return -1
	}
	resp.Body.Close()
	return resp.StatusCode
}

func loginEnv(t *testing.T) (Config, LoginOptions, string) {
	t.Helper()
	var calls atomic.Int32
	srv := fakeGoogle(t, map[string]string{canaryRefresh: "owner@example.com"}, &calls)
	t.Cleanup(srv.Close)
	return Config{APIKey: "test-key", WebURL: "https://quicknotes.example/"},
		LoginOptions{Timeout: 3 * time.Second, Tokens: Options{Endpoint: srv.URL}},
		filepath.Join(t.TempDir(), "QuickNotes", "mcp-session.json")
}

func TestLoginSavesSessionOwnerOnlyAndStopsListening(t *testing.T) {
	cfg, opt, path := loginEnv(t)
	var callbackURL string
	statusCh := make(chan int, 1)
	opt.OpenBrowser = browserThat(t, func(callback, state string) {
		callbackURL = callback
		statusCh <- postForm(t, callback, url.Values{"refresh_token": {canaryRefresh}, "state": {state}})
	})
	user, err := Login(context.Background(), cfg, path, opt)
	if err != nil {
		t.Fatal(err)
	}
	if status := <-statusCh; status != http.StatusOK || user.Email != "owner@example.com" {
		t.Errorf("status %d user %+v", status, user)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("session file %v mode %v", err, info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	var saved savedSession
	_ = json.Unmarshal(raw, &saved)
	if saved.RefreshToken != canaryRefresh || saved.UID != "uid-1" {
		t.Errorf("saved %+v", saved)
	}
	// single use: the listener is gone once Login returns
	if status := postForm(t, callbackURL, url.Values{"refresh_token": {"x"}, "state": {"y"}}); status != -1 {
		t.Errorf("callback still answering after login: %d", status)
	}
}

func TestLoginRejectsWrongOrMissingStateAndSavesNothing(t *testing.T) {
	for name, form := range map[string]func(state string) url.Values{
		"wrong state": func(string) url.Values {
			return url.Values{"refresh_token": {canaryRefresh}, "state": {strings.Repeat("0", 64)}}
		},
		"missing state": func(string) url.Values { return url.Values{"refresh_token": {canaryRefresh}} },
		"no token":      func(state string) url.Values { return url.Values{"state": {state}} },
	} {
		t.Run(name, func(t *testing.T) {
			cfg, opt, path := loginEnv(t)
			opt.OpenBrowser = browserThat(t, func(callback, state string) { postForm(t, callback, form(state)) })
			if _, err := Login(context.Background(), cfg, path, opt); err == nil {
				t.Fatal("expected an error")
			} else if strings.Contains(err.Error(), canaryRefresh) {
				t.Errorf("error leaks the token: %v", err)
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("nothing may be saved")
			}
		})
	}
}

func TestLoginRefusesGetOtherHostsAndRejectedTokens(t *testing.T) {
	cfg, opt, path := loginEnv(t)
	opt.OpenBrowser = browserThat(t, func(callback, state string) {
		// a GET (e.g. a link or image) and a request under another host name are ignored...
		if resp, err := http.Get(callback + "?refresh_token=x&state=" + state); err == nil {
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("GET status %d", resp.StatusCode)
			}
			resp.Body.Close()
		}
		req, _ := http.NewRequest(http.MethodPost, callback, strings.NewReader(url.Values{"refresh_token": {canaryRefresh}, "state": {state}}.Encode()))
		req.Host = "evil.example"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if resp, err := http.DefaultClient.Do(req); err == nil {
			if resp.StatusCode != http.StatusMisdirectedRequest {
				t.Errorf("other host status %d", resp.StatusCode)
			}
			resp.Body.Close()
		}
		// ...and a correct callback with a token Google rejects saves nothing
		postForm(t, callback, url.Values{"refresh_token": {"NOT-A-REAL-TOKEN"}, "state": {state}})
	})
	_, err := Login(context.Background(), cfg, path, opt)
	if err == nil || !strings.Contains(err.Error(), "did not accept") {
		t.Fatalf("want rejection, got %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("nothing may be saved")
	}
}

func TestLoginTimesOutAndListensOnLoopbackOnly(t *testing.T) {
	cfg, opt, path := loginEnv(t)
	opt.Timeout = 200 * time.Millisecond
	opt.OpenBrowser = func(link string) error {
		u, _ := url.Parse(link)
		cb, _ := url.Parse(u.Query().Get("return"))
		// not reachable on other interfaces: the socket is bound to 127.0.0.1 only
		if addrs, _ := net.InterfaceAddrs(); len(addrs) > 0 {
			for _, a := range addrs {
				ip, _, _ := net.ParseCIDR(a.String())
				if ip == nil || ip.IsLoopback() || ip.To4() == nil {
					continue
				}
				if c, err := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), cb.Port()), 100*time.Millisecond); err == nil {
					c.Close()
					t.Errorf("callback reachable on %s", ip)
				}
			}
		}
		return nil
	}
	start := time.Now()
	if _, err := Login(context.Background(), cfg, path, opt); err == nil || !strings.Contains(err.Error(), "no sign-in within") {
		t.Fatalf("want timeout, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("timeout took too long")
	}
	if _, err := Login(context.Background(), Config{APIKey: "k"}, path, opt); err == nil {
		t.Error("missing web address must fail")
	}
}
