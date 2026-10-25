package firestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TokenFunc returns a current ID token for the signed-in user.
type TokenFunc func(ctx context.Context) (string, error)

// Client talks to one Firestore database as the signed-in user, so the project's security rules
// apply to every request.
type Client struct {
	base    string // https://firestore.googleapis.com/v1/projects/<id>/databases/(default)/documents
	root    string // projects/<id>/databases/(default)/documents
	token   TokenFunc
	forget  func()
	http    *http.Client
	retries int
}

// Options tune a Client; zero values give production behaviour.
type Options struct {
	BaseURL    string // instead of https://firestore.googleapis.com (tests)
	HTTPClient *http.Client
	Forget     func() // called when Firestore rejects the ID token (HTTP 401), before one retry
}

// New returns a client for the given project.
func New(projectID string, token TokenFunc, opt Options) *Client {
	host := opt.BaseURL
	if host == "" {
		host = "https://firestore.googleapis.com"
	}
	root := "projects/" + projectID + "/databases/(default)/documents"
	c := &Client{base: strings.TrimRight(host, "/") + "/v1/" + root, root: root, token: token, forget: opt.Forget, http: opt.HTTPClient, retries: 1}
	if c.http == nil {
		c.http = &http.Client{Timeout: 15 * time.Second}
	}
	if c.forget == nil {
		c.forget = func() {}
	}
	return c
}

// Document is one Firestore document with decoded fields.
type Document struct {
	ID     string
	Name   string
	Fields map[string]any
}

// DocName returns the full resource name of collection/id, as commit writes need.
func (c *Client) DocName(collection, id string) string { return c.root + "/" + collection + "/" + id }

// Filter is one field condition of a structured query (op: EQUAL, ARRAY_CONTAINS, ...).
type Filter struct {
	Field string
	Op    string
	Value any
}

// RunQuery returns every document of collection matching all filters.
func (c *Client) RunQuery(ctx context.Context, collection string, filters []Filter) ([]Document, error) {
	where := make([]any, 0, len(filters))
	for _, f := range filters {
		v, err := Encode(f.Value)
		if err != nil {
			return nil, err
		}
		where = append(where, map[string]any{"fieldFilter": map[string]any{"field": map[string]any{"fieldPath": f.Field}, "op": f.Op, "value": v}})
	}
	query := map[string]any{"from": []any{map[string]any{"collectionId": collection}}}
	switch len(where) {
	case 0:
	case 1:
		query["where"] = where[0]
	default:
		query["where"] = map[string]any{"compositeFilter": map[string]any{"op": "AND", "filters": where}}
	}
	var rows []struct {
		Document *struct {
			Name   string                     `json:"name"`
			Fields map[string]json.RawMessage `json:"fields"`
		} `json:"document"`
	}
	if err := c.do(ctx, http.MethodPost, c.base+":runQuery", map[string]any{"structuredQuery": query}, &rows); err != nil {
		return nil, err
	}
	docs := make([]Document, 0, len(rows))
	for _, r := range rows {
		if r.Document == nil {
			continue // the response also carries rows that only report progress
		}
		fields, err := DecodeFields(r.Document.Fields)
		if err != nil {
			return nil, err
		}
		docs = append(docs, Document{ID: lastSegment(r.Document.Name), Name: r.Document.Name, Fields: fields})
	}
	return docs, nil
}

// Get reads collection/id. A missing document is an *APIError with Status 404.
func (c *Client) Get(ctx context.Context, collection, id string) (Document, error) {
	var doc struct {
		Name   string                     `json:"name"`
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := c.do(ctx, http.MethodGet, c.base+"/"+collection+"/"+url.PathEscape(id), nil, &doc); err != nil {
		return Document{}, err
	}
	fields, err := DecodeFields(doc.Fields)
	if err != nil {
		return Document{}, err
	}
	return Document{ID: lastSegment(doc.Name), Name: doc.Name, Fields: fields}, nil
}

// Create stores a new document with the given id (409 if it already exists).
func (c *Client) Create(ctx context.Context, collection, id string, fields map[string]any) error {
	encoded, err := EncodeFields(fields)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, c.base+"/"+collection+"?documentId="+url.QueryEscape(id), map[string]any{"fields": encoded}, nil)
}

// Write is one operation of an atomic commit.
type Write struct {
	// Update changes only the listed fields of an existing document (Fields keys = update mask).
	Document string
	Fields   map[string]any
	// Increment adds 1 to an integer field instead (Fields must be empty).
	Increment string
}

// Commit applies all writes atomically: either every write lands or none does.
func (c *Client) Commit(ctx context.Context, writes []Write) error {
	body := make([]any, 0, len(writes))
	for _, w := range writes {
		if w.Increment != "" {
			body = append(body, map[string]any{"transform": map[string]any{"document": w.Document,
				"fieldTransforms": []any{map[string]any{"fieldPath": w.Increment, "increment": map[string]any{"integerValue": "1"}}}}})
			continue
		}
		fields, err := EncodeFields(w.Fields)
		if err != nil {
			return err
		}
		body = append(body, map[string]any{
			"update":          map[string]any{"name": w.Document, "fields": fields},
			"updateMask":      map[string]any{"fieldPaths": SortedKeys(w.Fields)},
			"currentDocument": map[string]any{"exists": true}, // never create a note by accident
		})
	}
	return c.do(ctx, http.MethodPost, c.base+":commit", map[string]any{"writes": body}, nil)
}

// APIError is an unsuccessful Firestore answer. Message comes from Firestore and never
// contains credentials.
type APIError struct {
	Status  int    // HTTP status
	Code    string // e.g. PERMISSION_DENIED, NOT_FOUND, FAILED_PRECONDITION
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Firestore %d %s: %s", e.Status, e.Code, e.Message)
}

// IsNotFound reports whether err means the document does not exist (or is not visible to the user).
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && (e.Status == http.StatusNotFound || e.Code == "NOT_FOUND")
}

// IsPermissionDenied reports whether the security rules refused the request.
func IsPermissionDenied(err error) bool {
	var e *APIError
	return errors.As(err, &e) && (e.Status == http.StatusForbidden || e.Code == "PERMISSION_DENIED")
}

func (c *Client) do(ctx context.Context, method, endpoint string, in, out any) error {
	var payload []byte
	if in != nil {
		var err error
		if payload, err = json.Marshal(in); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		token, err := c.token(ctx)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
		if err != nil {
			return errors.New("could not build the Firestore request")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if in != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return errors.New("Firestore request was cancelled")
			}
			var ue *url.Error
			if errors.As(err, &ue) && ue.Timeout() {
				return errors.New("Firestore timed out; check your internet connection")
			}
			return errors.New("could not reach Firestore; check your internet connection")
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if readErr != nil {
			return errors.New("Firestore answer was cut off; try again")
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt < c.retries {
			c.forget() // the ID token was revoked or expired early: fetch a new one and retry once
			continue
		}
		if resp.StatusCode/100 != 2 {
			return apiError(resp.StatusCode, body)
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(body, out); err != nil {
			return errors.New("Firestore returned an unexpected answer")
		}
		return nil
	}
}

func apiError(status int, body []byte) error {
	var e struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	msg := e.Error.Message
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return &APIError{Status: status, Code: e.Error.Status, Message: msg}
}

func lastSegment(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}
