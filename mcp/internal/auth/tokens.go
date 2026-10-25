package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNotSignedIn means no saved session exists, or every saved one was rejected by Google.
var ErrNotSignedIn = errors.New("not signed in to QuickNotes: sign in from the QuickNotes Mac app (bubble menu → Sign in), " +
	"or run `quicknotes-mcp login` in a terminal, then try again")

// User is who the ID token belongs to.
type User struct {
	UID   string
	Email string
	Name  string
}

// SessionFile is one place a refresh token may be saved, in the Mac app's format:
// {"refreshToken": "...", "uid": "...", "email": "...", "name": "..."}.
type SessionFile struct {
	Path  string
	Label string // for messages, e.g. "Mac app session"
}

// DefaultSessions returns the Mac app's session file followed by the server's own login file.
func DefaultSessions(home string) []SessionFile {
	dir := filepath.Join(home, "Library", "Application Support", "QuickNotes")
	return []SessionFile{
		{Path: filepath.Join(dir, "session.json"), Label: "Mac app session"},
		{Path: filepath.Join(dir, "mcp-session.json"), Label: "quicknotes-mcp login"},
	}
}

// TokenSource hands out ID tokens, refreshing them shortly before they expire.
// It is safe for concurrent use.
type TokenSource struct {
	cfg      Config
	sessions []SessionFile
	http     *http.Client
	endpoint string // securetoken token URL (overridable for tests)
	now      func() time.Time

	mu      sync.Mutex
	idToken string
	expiry  time.Time
	user    User
}

// Options tune a TokenSource; zero values give production behaviour.
type Options struct {
	HTTPClient *http.Client
	Endpoint   string
	Now        func() time.Time
}

// NewTokenSource returns a TokenSource that tries sessions in order.
func NewTokenSource(cfg Config, sessions []SessionFile, opt Options) *TokenSource {
	s := &TokenSource{cfg: cfg, sessions: sessions, http: opt.HTTPClient, endpoint: opt.Endpoint, now: opt.Now}
	if s.http == nil {
		s.http = &http.Client{Timeout: 10 * time.Second}
	}
	if s.endpoint == "" {
		s.endpoint = "https://securetoken.googleapis.com/v1/token"
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// Token returns a valid ID token and its user.
func (s *TokenSource) Token(ctx context.Context) (string, User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idToken != "" && s.now().Add(time.Minute).Before(s.expiry) {
		return s.idToken, s.user, nil
	}
	var problems []string
	for _, sf := range s.sessions {
		refresh, saved, err := readSession(sf.Path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", sf.Label, err))
			continue
		}
		id, user, ttl, err := s.exchange(ctx, refresh)
		if errors.Is(err, errRejected) {
			problems = append(problems, sf.Label+" was rejected by Google (signed out or expired)")
			continue
		}
		if err != nil {
			return "", User{}, err // network trouble: don't silently fall through to another account
		}
		if user.Email == "" {
			user.Email = saved.Email
		}
		if user.Name == "" {
			user.Name = saved.Name
		}
		s.idToken, s.user, s.expiry = id, user, s.now().Add(ttl)
		return id, user, nil
	}
	if len(problems) > 0 {
		return "", User{}, fmt.Errorf("%w (%s)", ErrNotSignedIn, strings.Join(problems, "; "))
	}
	return "", User{}, ErrNotSignedIn
}

// Forget drops the cached ID token, e.g. after Firestore said it is no longer valid.
func (s *TokenSource) Forget() {
	s.mu.Lock()
	s.idToken = ""
	s.mu.Unlock()
}

type savedSession struct {
	RefreshToken string `json:"refreshToken"`
	UID          string `json:"uid"`
	Email        string `json:"email"`
	Name         string `json:"name"`
}

// readSession only reads the file; it never changes it.
func readSession(path string) (string, savedSession, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", savedSession{}, os.ErrNotExist
		}
		return "", savedSession{}, errors.New("could not read the saved session")
	}
	var s savedSession
	if err := json.Unmarshal(raw, &s); err != nil || s.RefreshToken == "" {
		return "", savedSession{}, errors.New("saved session is unreadable or empty")
	}
	return s.RefreshToken, s, nil
}

var errRejected = errors.New("refresh token rejected")

// exchange swaps a refresh token for an ID token at Google's securetoken service.
func (s *TokenSource) exchange(ctx context.Context, refresh string) (string, User, time.Duration, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint+"?key="+url.QueryEscape(s.cfg.APIKey), strings.NewReader(form.Encode()))
	if err != nil {
		return "", User{}, 0, errors.New("could not build the sign-in request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		return "", User{}, 0, networkError(ctx, "Google sign-in", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", User{}, 0, errRejected
	}
	if resp.StatusCode != http.StatusOK {
		return "", User{}, 0, fmt.Errorf("Google sign-in answered HTTP %d; try again in a moment", resp.StatusCode)
	}
	var body struct {
		IDToken   string `json:"id_token"`
		ExpiresIn string `json:"expires_in"`
		UserID    string `json:"user_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.IDToken == "" {
		return "", User{}, 0, errors.New("Google sign-in returned an unexpected answer")
	}
	ttl := time.Hour
	if secs, err := strconv.Atoi(body.ExpiresIn); err == nil && secs > 0 {
		ttl = time.Duration(secs) * time.Second
	}
	claims := jwtClaims(body.IDToken)
	user := User{UID: body.UserID, Email: strings.ToLower(claims.Email), Name: claims.Name}
	if user.UID == "" {
		user.UID = claims.Subject
	}
	return body.IDToken, user, ttl, nil
}

type claims struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
	Name    string `json:"name"`
}

// jwtClaims reads (without verifying) the payload of a JWT that came straight from Google over TLS.
func jwtClaims(token string) claims {
	var c claims
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return c
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err == nil {
		_ = json.Unmarshal(payload, &c)
	}
	return c
}

// networkError describes a failed request without echoing the URL (which carries the API key).
func networkError(ctx context.Context, service string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s request was cancelled", service)
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.Timeout() {
		return fmt.Errorf("%s timed out; check your internet connection", service)
	}
	return fmt.Errorf("could not reach %s; check your internet connection", service)
}
