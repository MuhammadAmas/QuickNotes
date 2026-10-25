package firestore

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const canaryToken = "CANARY-IDTOKEN-55aa"

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := map[string]any{
		"s": "milk", "b": true, "i": 3, "whole": float64(1760000000123), "frac": 1.5, "null": nil,
		"list": []any{"a", int64(2)}, "empty": []any{}, "emails": []string{"x@y.z"},
		"map": map[string]any{"id": "i1", "done": false},
	}
	fields, err := EncodeFields(in)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(fields)
	var back map[string]json.RawMessage
	_ = json.Unmarshal(raw, &back)
	got, err := DecodeFields(back)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"s": "milk", "b": true, "i": int64(3), "whole": int64(1760000000123), "frac": 1.5, "null": nil,
		"list": []any{"a", int64(2)}, "empty": []any{}, "emails": []any{"x@y.z"},
		"map": map[string]any{"id": "i1", "done": false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip:\n got %#v\nwant %#v", got, want)
	}
	if _, err := Encode(struct{}{}); err == nil {
		t.Error("unsupported types must be rejected")
	}
}

type recorded struct {
	method, path, query, auth string
	body                      map[string]any
}

// fakeFirestore records requests and answers with the given handler.
func fakeFirestore(t *testing.T, answer func(r recorded) (int, string)) (*httptest.Server, *[]recorded) {
	t.Helper()
	var log []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rec := recorded{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization")}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &rec.body)
		}
		log = append(log, rec)
		status, body := answer(rec)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &log
}

func tokenFn(token string) TokenFunc {
	return func(context.Context) (string, error) { return token, nil }
}

func TestRunQueryBuildsCompositeFilterAndDecodes(t *testing.T) {
	srv, log := fakeFirestore(t, func(recorded) (int, string) {
		return 200, `[{"document":{"name":"projects/p/databases/(default)/documents/qn_notes/n1","fields":{"content":{"stringValue":"hi"},"times_moved":{"integerValue":"2"}}}},{"readTime":"x"}]`
	})
	c := New("p", tokenFn(canaryToken), Options{BaseURL: srv.URL})
	docs, err := c.RunQuery(context.Background(), "qn_notes", []Filter{{"ownerId", "EQUAL", "u1"}, {"note_date", "EQUAL", "2026-10-17"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].ID != "n1" || docs[0].Fields["content"] != "hi" || docs[0].Fields["times_moved"] != int64(2) {
		t.Fatalf("docs = %+v", docs)
	}
	r := (*log)[0]
	if r.method != "POST" || !strings.HasSuffix(r.path, "/v1/projects/p/databases/(default)/documents:runQuery") || r.auth != "Bearer "+canaryToken {
		t.Errorf("request = %+v", r)
	}
	where := r.body["structuredQuery"].(map[string]any)["where"].(map[string]any)["compositeFilter"].(map[string]any)
	if where["op"] != "AND" || len(where["filters"].([]any)) != 2 {
		t.Errorf("where = %v", where)
	}
}

func TestCommitSendsUpdateMaskPreconditionAndIncrement(t *testing.T) {
	srv, log := fakeFirestore(t, func(recorded) (int, string) { return 200, `{}` })
	c := New("p", tokenFn("t"), Options{BaseURL: srv.URL})
	err := c.Commit(context.Background(), []Write{
		{Document: c.DocName("qn_notes", "n1"), Fields: map[string]any{"status": "done", "completed_at": "ts", "updated_at": "ts"}},
		{Document: c.DocName("users", "u1"), Increment: "notesCreated"},
	})
	if err != nil {
		t.Fatal(err)
	}
	writes := (*log)[0].body["writes"].([]any)
	update := writes[0].(map[string]any)
	mask := update["updateMask"].(map[string]any)["fieldPaths"]
	if !reflect.DeepEqual(mask, []any{"completed_at", "status", "updated_at"}) {
		t.Errorf("mask = %v", mask)
	}
	if update["currentDocument"].(map[string]any)["exists"] != true {
		t.Error("updates must require an existing document")
	}
	inc := writes[1].(map[string]any)["transform"].(map[string]any)
	if inc["document"] != "projects/p/databases/(default)/documents/users/u1" {
		t.Errorf("increment doc = %v", inc["document"])
	}
}

func TestCreateUsesDocumentID(t *testing.T) {
	srv, log := fakeFirestore(t, func(recorded) (int, string) { return 200, `{}` })
	c := New("p", tokenFn("t"), Options{BaseURL: srv.URL})
	if err := c.Create(context.Background(), "qn_notes", "abc-123", map[string]any{"content": "x"}); err != nil {
		t.Fatal(err)
	}
	r := (*log)[0]
	if !strings.HasSuffix(r.path, "/documents/qn_notes") || r.query != "documentId=abc-123" {
		t.Errorf("request = %+v", r)
	}
}

func TestUnauthorizedRetriesOnceWithFreshToken(t *testing.T) {
	var forgot atomic.Int32
	srv, log := fakeFirestore(t, func(r recorded) (int, string) {
		if r.auth == "Bearer old" {
			return 401, `{"error":{"status":"UNAUTHENTICATED","message":"expired"}}`
		}
		return 200, `{"name":"x/qn_notes/n1","fields":{}}`
	})
	token := "old"
	c := New("p", func(context.Context) (string, error) { return token, nil }, Options{BaseURL: srv.URL, Forget: func() { forgot.Add(1); token = "new" }})
	if _, err := c.Get(context.Background(), "qn_notes", "n1"); err != nil {
		t.Fatal(err)
	}
	if forgot.Load() != 1 || len(*log) != 2 {
		t.Errorf("forgot=%d requests=%d", forgot.Load(), len(*log))
	}
}

func TestErrorsAreClassifiedAndNeverLeakTheToken(t *testing.T) {
	srv, _ := fakeFirestore(t, func(r recorded) (int, string) {
		switch {
		case strings.HasSuffix(r.path, "/missing"):
			return 404, `{"error":{"status":"NOT_FOUND","message":"Document not found"}}`
		case strings.HasSuffix(r.path, "/locked"):
			return 403, `{"error":{"status":"PERMISSION_DENIED","message":"Missing or insufficient permissions."}}`
		default:
			return 500, `oops`
		}
	})
	c := New("p", tokenFn(canaryToken), Options{BaseURL: srv.URL})
	_, err := c.Get(context.Background(), "qn_notes", "missing")
	if !IsNotFound(err) {
		t.Errorf("want not found, got %v", err)
	}
	_, err = c.Get(context.Background(), "qn_notes", "locked")
	if !IsPermissionDenied(err) {
		t.Errorf("want permission denied, got %v", err)
	}
	_, err500 := c.Get(context.Background(), "qn_notes", "other")
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(200 * time.Millisecond) }))
	defer slow.Close()
	_, errTimeout := New("p", tokenFn(canaryToken), Options{BaseURL: slow.URL, HTTPClient: &http.Client{Timeout: 30 * time.Millisecond}}).Get(context.Background(), "qn_notes", "n")
	_, errDown := New("p", tokenFn(canaryToken), Options{BaseURL: "http://127.0.0.1:1"}).Get(context.Background(), "qn_notes", "n")
	for _, e := range []error{err, err500, errTimeout, errDown} {
		if e == nil || strings.Contains(e.Error(), canaryToken) || strings.Contains(e.Error(), "127.0.0.1") {
			t.Errorf("bad error: %v", e)
		}
	}
	if !strings.Contains(errTimeout.Error(), "timed out") {
		t.Errorf("timeout message: %v", errTimeout)
	}
}
