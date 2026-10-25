// Package fakefirebase is an in-memory stand-in for Google's securetoken service and the
// Firestore REST endpoints QuickNotes uses, for tests only. It enforces the parts of
// firestore.rules.template that the MCP tools rely on (owner vs shared-with access and the
// fields a shared-with person may change) and records every request.
package fakefirebase

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/MuhammadAmas/QuickNotes/mcp/internal/firestore"
)

// Account is a signed-in person known to the fake.
type Account struct {
	UID, Email, Name string
	RefreshToken     string // accepted by the fake securetoken endpoint
	IDToken          string // what the fake hands out and then accepts as Bearer
}

// Request is one recorded call.
type Request struct {
	Method, Path, Query string
	Header              http.Header
	Body                string
}

// Fake is the running fake. Use URL for both Options.Endpoint (securetoken: URL+"/token")
// and the Firestore base URL.
type Fake struct {
	Server  *httptest.Server
	Project string

	mu        sync.Mutex
	accounts  []Account
	docs      map[string]map[string]any // "collection/id" -> decoded fields
	requests  []Request
	FailNext  map[string]int // path suffix -> HTTP status to return once (e.g. ":commit": 500)
	TokenDown bool           // securetoken answers 503
}

// allowedSharedKeys mirrors the rules: what someone a note is shared with may change.
var allowedSharedKeys = []string{"content", "status", "note_date", "updated_at", "edited_at", "completed_at", "original_date", "times_moved", "order", "checklist"}

// New starts a fake with the given accounts.
func New(project string, accounts ...Account) *Fake {
	f := &Fake{Project: project, accounts: accounts, docs: map[string]map[string]any{}, FailNext: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// Close stops the server.
func (f *Fake) Close() { f.Server.Close() }

// TokenURL is the securetoken endpoint to give auth.Options.Endpoint.
func (f *Fake) TokenURL() string { return f.Server.URL + "/token" }

// Put stores a document directly (test setup).
func (f *Fake) Put(collection, id string, fields map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.docs[collection+"/"+id] = normalize(fields)
}

// Doc returns a copy of a stored document's fields, or nil.
func (f *Fake) Doc(collection, id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.docs[collection+"/"+id]
	if d == nil {
		return nil
	}
	out := map[string]any{}
	for k, v := range d {
		out[k] = v
	}
	return out
}

// Docs returns the ids of a collection's documents, sorted.
func (f *Fake) Docs(collection string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for key := range f.docs {
		if c, id, _ := strings.Cut(key, "/"); c == collection {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// Requests returns the recorded calls so far.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// FirestoreRequests counts recorded Firestore (non-token) calls.
func (f *Fake) FirestoreRequests() int {
	n := 0
	for _, r := range f.Requests() {
		if r.Path != "/token" {
			n++
		}
	}
	return n
}

// IDTokenFor builds a JWT-shaped token carrying the account's claims, ending in suffix.
func IDTokenFor(a Account, suffix string) string {
	payload, _ := json.Marshal(map[string]string{"sub": a.UID, "email": a.Email, "name": a.Name})
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + "." + suffix
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: string(raw)})
	for suffix, status := range f.FailNext {
		if strings.HasSuffix(r.URL.Path, suffix) {
			delete(f.FailNext, suffix)
			f.mu.Unlock()
			writeErr(w, status, "INTERNAL", "injected failure")
			return
		}
	}
	f.mu.Unlock()

	if r.URL.Path == "/token" {
		f.token(w, string(raw))
		return
	}
	who := f.caller(r.Header.Get("Authorization"))
	if who == nil {
		writeErr(w, 401, "UNAUTHENTICATED", "Request had invalid authentication credentials.")
		return
	}
	prefix := "/v1/projects/" + f.Project + "/databases/(default)/documents"
	path := strings.TrimPrefix(r.URL.Path, prefix)
	switch {
	case r.Method == http.MethodPost && path == ":runQuery":
		f.runQuery(w, who, raw)
	case r.Method == http.MethodPost && path == ":commit":
		f.commit(w, who, raw)
	case r.Method == http.MethodGet && strings.Count(path, "/") == 2:
		f.get(w, who, strings.TrimPrefix(path, "/"))
	case r.Method == http.MethodPost && strings.Count(path, "/") == 1:
		f.create(w, who, strings.TrimPrefix(path, "/"), r.URL.Query().Get("documentId"), raw)
	default:
		writeErr(w, 404, "NOT_FOUND", "unknown endpoint")
	}
}

func (f *Fake) token(w http.ResponseWriter, form string) {
	f.mu.Lock()
	down := f.TokenDown
	f.mu.Unlock()
	if down {
		w.WriteHeader(503)
		return
	}
	values, _ := url.ParseQuery(form)
	for _, a := range f.accounts {
		if a.RefreshToken != "" && values.Get("refresh_token") == a.RefreshToken {
			_ = json.NewEncoder(w).Encode(map[string]string{"id_token": a.IDToken, "expires_in": "3600", "user_id": a.UID, "refresh_token": a.RefreshToken})
			return
		}
	}
	w.WriteHeader(400)
	_, _ = io.WriteString(w, `{"error":{"message":"INVALID_REFRESH_TOKEN"}}`)
}

func (f *Fake) caller(header string) *Account {
	token := strings.TrimPrefix(header, "Bearer ")
	for i := range f.accounts {
		if f.accounts[i].IDToken != "" && f.accounts[i].IDToken == token {
			return &f.accounts[i]
		}
	}
	return nil
}

func canRead(a *Account, doc map[string]any) bool {
	if doc["ownerId"] == a.UID {
		return true
	}
	shared, _ := doc["sharedWith"].([]any)
	return slices.Contains(shared, any(a.Email))
}

func (f *Fake) runQuery(w http.ResponseWriter, who *Account, raw []byte) {
	var q struct {
		StructuredQuery struct {
			From []struct {
				CollectionID string `json:"collectionId"`
			} `json:"from"`
			Where json.RawMessage `json:"where"`
		} `json:"structuredQuery"`
	}
	_ = json.Unmarshal(raw, &q)
	filters := parseWhere(q.StructuredQuery.Where)
	collection := q.StructuredQuery.From[0].CollectionID
	// Firestore rules reject a list query unless every result is readable; the apps' queries
	// (by ownerId == me, or sharedWith contains me) always are, so the fake checks that shape.
	if !queryIsSafe(who, filters) {
		writeErr(w, 403, "PERMISSION_DENIED", "Missing or insufficient permissions.")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := []any{}
	keys := make([]string, 0, len(f.docs))
	for k := range f.docs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, key := range keys {
		c, id, _ := strings.Cut(key, "/")
		if c != collection || !matches(f.docs[key], filters) {
			continue
		}
		rows = append(rows, map[string]any{"document": f.encodeDoc(c, id, f.docs[key])})
	}
	rows = append(rows, map[string]any{"readTime": "2026-10-17T00:00:00Z"})
	_ = json.NewEncoder(w).Encode(rows)
}

type filter struct {
	field, op string
	value     any
}

func parseWhere(raw json.RawMessage) []filter {
	if len(raw) == 0 {
		return nil
	}
	var w struct {
		FieldFilter *struct {
			Field struct {
				FieldPath string `json:"fieldPath"`
			} `json:"field"`
			Op    string          `json:"op"`
			Value json.RawMessage `json:"value"`
		} `json:"fieldFilter"`
		CompositeFilter *struct {
			Filters []json.RawMessage `json:"filters"`
		} `json:"compositeFilter"`
	}
	_ = json.Unmarshal(raw, &w)
	if w.FieldFilter != nil {
		v, _ := firestore.Decode(w.FieldFilter.Value)
		return []filter{{w.FieldFilter.Field.FieldPath, w.FieldFilter.Op, v}}
	}
	var out []filter
	if w.CompositeFilter != nil {
		for _, sub := range w.CompositeFilter.Filters {
			out = append(out, parseWhere(sub)...)
		}
	}
	return out
}

func queryIsSafe(who *Account, filters []filter) bool {
	for _, fl := range filters {
		if (fl.field == "ownerId" && fl.op == "EQUAL" && fl.value == who.UID) ||
			(fl.field == "sharedWith" && fl.op == "ARRAY_CONTAINS" && fl.value == who.Email) {
			return true
		}
	}
	return false
}

func matches(doc map[string]any, filters []filter) bool {
	for _, fl := range filters {
		switch fl.op {
		case "EQUAL":
			if doc[fl.field] != fl.value {
				return false
			}
		case "ARRAY_CONTAINS":
			list, _ := doc[fl.field].([]any)
			if !slices.Contains(list, fl.value) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (f *Fake) get(w http.ResponseWriter, who *Account, key string) {
	f.mu.Lock()
	doc, ok := f.docs[key]
	f.mu.Unlock()
	if !ok {
		writeErr(w, 404, "NOT_FOUND", "Document not found")
		return
	}
	if !canRead(who, doc) {
		writeErr(w, 403, "PERMISSION_DENIED", "Missing or insufficient permissions.")
		return
	}
	c, id, _ := strings.Cut(key, "/")
	_ = json.NewEncoder(w).Encode(f.encodeDoc(c, id, doc))
}

func (f *Fake) create(w http.ResponseWriter, who *Account, collection, id string, raw []byte) {
	var body struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	_ = json.Unmarshal(raw, &body)
	fields, err := firestore.DecodeFields(body.Fields)
	if err != nil || id == "" {
		writeErr(w, 400, "INVALID_ARGUMENT", "bad document")
		return
	}
	if collection != "qn_notes" || fields["ownerId"] != who.UID || !validNote(fields) {
		writeErr(w, 403, "PERMISSION_DENIED", "Missing or insufficient permissions.")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.docs[collection+"/"+id]; exists {
		writeErr(w, 409, "ALREADY_EXISTS", "Document already exists")
		return
	}
	f.docs[collection+"/"+id] = fields
	_ = json.NewEncoder(w).Encode(f.encodeDoc(collection, id, fields))
}

func (f *Fake) commit(w http.ResponseWriter, who *Account, raw []byte) {
	var body struct {
		Writes []struct {
			Update *struct {
				Name   string                     `json:"name"`
				Fields map[string]json.RawMessage `json:"fields"`
			} `json:"update"`
			UpdateMask *struct {
				FieldPaths []string `json:"fieldPaths"`
			} `json:"updateMask"`
			CurrentDocument *struct {
				Exists bool `json:"exists"`
			} `json:"currentDocument"`
			Transform *struct {
				Document        string `json:"document"`
				FieldTransforms []struct {
					FieldPath string `json:"fieldPath"`
				} `json:"fieldTransforms"`
			} `json:"transform"`
		} `json:"writes"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeErr(w, 400, "INVALID_ARGUMENT", "bad commit")
		return
	}
	root := "projects/" + f.Project + "/databases/(default)/documents/"
	f.mu.Lock()
	defer f.mu.Unlock()
	staged := map[string]map[string]any{}
	for _, wr := range body.Writes {
		switch {
		case wr.Transform != nil:
			key := strings.TrimPrefix(wr.Transform.Document, root)
			if key != "users/"+who.UID {
				writeErr(w, 403, "PERMISSION_DENIED", "Missing or insufficient permissions.")
				return
			}
			doc := cloneDoc(f.docs[key])
			if doc == nil {
				doc = map[string]any{"uid": who.UID}
			}
			for _, t := range wr.Transform.FieldTransforms {
				n, _ := doc[t.FieldPath].(int64)
				doc[t.FieldPath] = n + 1
			}
			staged[key] = doc
		case wr.Update != nil:
			key := strings.TrimPrefix(wr.Update.Name, root)
			old, exists := f.docs[key]
			if wr.CurrentDocument != nil && wr.CurrentDocument.Exists && !exists {
				writeErr(w, 404, "NOT_FOUND", "No document to update")
				return
			}
			fields, err := firestore.DecodeFields(wr.Update.Fields)
			if err != nil || wr.UpdateMask == nil {
				writeErr(w, 400, "INVALID_ARGUMENT", "update needs fields and a mask")
				return
			}
			doc := cloneDoc(old)
			for _, path := range wr.UpdateMask.FieldPaths {
				if v, ok := fields[path]; ok {
					doc[path] = v
				} else {
					delete(doc, path)
				}
			}
			isOwner := old["ownerId"] == who.UID && doc["ownerId"] == old["ownerId"]
			sharedOK := canRead(who, old) && onlyAllowedKeys(wr.UpdateMask.FieldPaths)
			if !validNote(doc) || !(isOwner || sharedOK) {
				writeErr(w, 403, "PERMISSION_DENIED", "Missing or insufficient permissions.")
				return
			}
			staged[key] = doc
		}
	}
	for key, doc := range staged { // atomic: only after every write passed
		f.docs[key] = doc
	}
	_, _ = io.WriteString(w, `{"writeResults":[]}`)
}

func onlyAllowedKeys(paths []string) bool {
	for _, p := range paths {
		if !slices.Contains(allowedSharedKeys, p) {
			return false
		}
	}
	return true
}

// validNote mirrors validNote() in the security rules.
func validNote(d map[string]any) bool {
	content, _ := d["content"].(string)
	date, _ := d["note_date"].(string)
	status, _ := d["status"].(string)
	_, owner := d["ownerId"].(string)
	shared, sharedOK := d["sharedWith"].([]any)
	if v, ok := d["checklist"]; ok {
		if list, isList := v.([]any); !isList || len(list) > 100 {
			return false
		}
	}
	if v, ok := d["deleted_at"]; ok && v != nil {
		if _, isStr := v.(string); !isStr {
			return false
		}
	}
	return len(content) > 0 && len([]rune(content)) <= 10000 && len(date) == 10 && (status == "open" || status == "done") &&
		owner && sharedOK && len(shared) <= 50
}

func (f *Fake) encodeDoc(collection, id string, fields map[string]any) map[string]any {
	enc, err := firestore.EncodeFields(fields)
	if err != nil {
		panic(fmt.Sprintf("fake: cannot encode stored doc: %v", err))
	}
	return map[string]any{"name": "projects/" + f.Project + "/databases/(default)/documents/" + collection + "/" + id, "fields": enc}
}

func cloneDoc(d map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range d {
		out[k] = v
	}
	return out
}

// normalize stores test fixtures the way decoded Firestore values look (int64, []any).
func normalize(fields map[string]any) map[string]any {
	enc, err := firestore.EncodeFields(fields)
	if err != nil {
		panic(err)
	}
	raw, _ := json.Marshal(enc)
	var back map[string]json.RawMessage
	_ = json.Unmarshal(raw, &back)
	out, _ := firestore.DecodeFields(back)
	return out
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": status, "status": code, "message": msg}})
}
