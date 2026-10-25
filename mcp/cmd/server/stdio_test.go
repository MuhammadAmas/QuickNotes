package main

// Oracles "tokens never leak" and "Mac sign-in is never disturbed", against the real compiled
// binary over real stdio: the exact process an AI client starts.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MuhammadAmas/QuickNotes/mcp/internal/fakefirebase"
)

// buildBinary compiles the server once per test run.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "quicknotes-mcp")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("blocked: build failed: %v\n%s", err, out)
	}
	return bin
}

// macHome creates a fake home folder holding a Mac app session with refresh token and a fixed mtime.
func macHome(t *testing.T, refresh string) (home, sessionPath string) {
	t.Helper()
	home = t.TempDir()
	dir := filepath.Join(home, "Library", "Application Support", "QuickNotes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionPath = filepath.Join(dir, "session.json")
	b, _ := json.Marshal(map[string]string{"refreshToken": refresh, "uid": owner.UID, "email": owner.Email, "name": owner.Name})
	if err := os.WriteFile(sessionPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	_ = os.Chtimes(sessionPath, old, old)
	return home, sessionPath
}

type stdioServer struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  *bufio.Scanner
	stdout bytes.Buffer // every byte the server wrote to stdout
	stderr bytes.Buffer
	nextID int
}

func startStdio(t *testing.T, bin, home, backend string) *stdioServer {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = []string{"HOME=" + home, "QUICKNOTES_API_KEY=test-key", "QUICKNOTES_PROJECT_ID=" + project, "QUICKNOTES_TEST_BACKEND=" + backend, "TZ=Asia/Karachi"}
	s := &stdioServer{cmd: cmd}
	cmd.Stderr = &s.stderr
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	s.stdin = stdin
	s.lines = bufio.NewScanner(io.TeeReader(stdout, &s.stdout))
	s.lines.Buffer(make([]byte, 1<<20), 1<<24)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.cmd.Process.Kill(); _ = s.cmd.Wait() })
	s.request(t, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "oracle", "version": "0"}})
	s.send(t, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return s
}

func (s *stdioServer) send(t *testing.T, msg map[string]any) {
	t.Helper()
	b, _ := json.Marshal(msg)
	if _, err := s.stdin.Write(append(b, '\n')); err != nil {
		t.Fatalf("write to server: %v (stderr: %s)", err, s.stderr.String())
	}
}

// request sends one JSON-RPC request and returns its result (or error) object.
func (s *stdioServer) request(t *testing.T, method string, params map[string]any) map[string]any {
	t.Helper()
	s.nextID++
	s.send(t, map[string]any{"jsonrpc": "2.0", "id": s.nextID, "method": method, "params": params})
	for s.lines.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(s.lines.Bytes(), &msg); err != nil {
			t.Fatalf("stdout line is not JSON-RPC: %q", s.lines.Text())
		}
		if id, ok := msg["id"].(float64); ok && int(id) == s.nextID {
			return msg
		}
	}
	t.Fatalf("server closed stdout (stderr: %s)", s.stderr.String())
	return nil
}

func (s *stdioServer) callTool(t *testing.T, name string, args map[string]any) (isError bool, text string) {
	t.Helper()
	msg := s.request(t, "tools/call", map[string]any{"name": name, "arguments": args})
	result, _ := msg["result"].(map[string]any)
	if result == nil {
		return true, fmt.Sprint(msg["error"])
	}
	isError, _ = result["isError"].(bool)
	if content, ok := result["content"].([]any); ok && len(content) > 0 {
		text, _ = content[0].(map[string]any)["text"].(string)
	}
	return isError, text
}

func TestStdioNeverLeaksTokens(t *testing.T) {
	bin := buildBinary(t)
	fake := fakefirebase.New(project, owner, friend)
	defer fake.Close()
	seed(fake)
	backend := strings.Replace(fake.Server.URL, "localhost", "127.0.0.1", 1)

	home, session := macHome(t, canaryRefresh)
	before, _ := os.ReadFile(session)
	srv := startStdio(t, bin, home, backend)

	if isErr, text := srv.callTool(t, "list_notes", map[string]any{}); isErr || !strings.Contains(text, "Call Sam") {
		t.Fatalf("list_notes over stdio: %v %s", isErr, text)
	}
	srv.callTool(t, "add_note", map[string]any{"content": "stdio note"})
	srv.callTool(t, "get_note", map[string]any{"id": "missing-note"})
	srv.callTool(t, "get_note", map[string]any{"id": "friend-private"})
	fake.FailNext[":runQuery"] = 500
	if isErr, _ := srv.callTool(t, "list_notes", map[string]any{}); !isErr {
		t.Error("a Firestore 500 should be a tool error")
	}
	fake.FailNext[":commit"] = 403
	srv.callTool(t, "set_status", map[string]any{"id": "today-open", "status": "done"})
	srv.request(t, "resources/read", map[string]any{"uri": "quicknotes://today"})
	srv.request(t, "prompts/get", map[string]any{"name": "weekly-review"})

	// a second server whose saved session Google rejects
	badHome, badSession := macHome(t, "REJECTED-"+canaryRefresh)
	badBefore := stateOf(t, badSession)
	bad := startStdio(t, bin, badHome, backend)
	isErr, text := bad.callTool(t, "list_notes", map[string]any{})
	if !isErr || !strings.Contains(text, "not signed in") || !strings.Contains(text, "quicknotes-mcp login") {
		t.Errorf("rejected session should say how to sign in: %v %q", isErr, text)
	}
	_ = srv.stdin.Close()
	_ = bad.stdin.Close()
	_ = srv.cmd.Wait()
	_ = bad.cmd.Wait()

	for name, out := range map[string]string{
		"stdout": srv.stdout.String() + bad.stdout.String(),
		"stderr": srv.stderr.String() + bad.stderr.String(),
	} {
		for _, secret := range []string{canaryRefresh, canaryID, "test-key"} {
			if strings.Contains(out, secret) {
				t.Errorf("%s contains %q", name, secret)
			}
		}
	}
	// stdout carries only JSON-RPC frames
	for _, line := range strings.Split(strings.TrimSpace(srv.stdout.String()+bad.stdout.String()), "\n") {
		var msg map[string]any
		if json.Unmarshal([]byte(line), &msg) != nil || msg["jsonrpc"] != "2.0" {
			t.Errorf("non-protocol stdout line: %q", line)
		}
	}
	// the Mac app's session files are byte-for-byte, mode and mtime unchanged
	if after, _ := os.ReadFile(session); !bytes.Equal(before, after) {
		t.Error("Mac session changed")
	}
	assertSame(t, badSession, badBefore)
	if _, err := os.Stat(filepath.Join(home, "Library", "Application Support", "QuickNotes", "mcp-session.json")); err == nil {
		t.Error("serving must not create a login file")
	}
}

func TestTestBackendOnlyAcceptsLoopback(t *testing.T) {
	for _, raw := range []string{"https://127.0.0.1:9", "http://evil.example:80", "http://localhost:9", "http://127.0.0.1", "http://u@127.0.0.1:9", "http://127.0.0.1:9/x"} {
		if _, _, err := testBackend(raw); err == nil {
			t.Errorf("%s should be rejected", raw)
		}
	}
	if a, f, err := testBackend("http://127.0.0.1:4321"); err != nil || a.Endpoint != "http://127.0.0.1:4321/token" || f.BaseURL != "http://127.0.0.1:4321" {
		t.Errorf("loopback rejected: %v", err)
	}
}

type fileState struct {
	data  []byte
	mode  os.FileMode
	mtime time.Time
}

func stateOf(t *testing.T, path string) fileState {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	return fileState{data, info.Mode().Perm(), info.ModTime()}
}

func assertSame(t *testing.T, path string, before fileState) {
	t.Helper()
	after := stateOf(t, path)
	if !bytes.Equal(before.data, after.data) || before.mode != after.mode || !before.mtime.Equal(after.mtime) {
		t.Errorf("%s changed (mode %v→%v, mtime %v→%v)", filepath.Base(path), before.mode, after.mode, before.mtime, after.mtime)
	}
}
