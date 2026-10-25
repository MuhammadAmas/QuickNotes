package notes

// Oracle "Go note rules match notes-model.js": runs the same fixtures through the real
// web/notes-model.js (under Node, clock frozen) and through this package, then compares.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fixtures struct {
	Now       string            `json:"now"`
	User      map[string]string `json:"user"`
	Parse     []string          `json:"parse"`
	ManyItems int               `json:"many_items"`
	NewNote   []struct {
		Content string `json:"content"`
		Date    string `json:"date"`
		Parse   string `json:"parse"`
	} `json:"new_note"`
	Patch []struct {
		Note    Note                       `json:"note"`
		Changes map[string]json.RawMessage `json:"changes"`
	} `json:"patch"`
	Emails [][]string `json:"emails"`
}

const scenario = "../../oracle-tests/scenarios/notes-core-js-parity"

func TestParityWithNotesModelJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("blocked: node is not installed, so notes-model.js cannot be run")
	}
	raw, err := os.ReadFile(filepath.Join(scenario, "fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx fixtures
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339Nano, fx.Now)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(node, filepath.Join(scenario, "reference.mjs"), filepath.Join(scenario, "fixtures.json"), "../../../web/notes-model.js")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	jsOut, err := cmd.Output()
	if err != nil {
		t.Fatalf("blocked: reference run failed: %v\n%s", err, stderr.String())
	}

	// Go side, same inputs.
	user := User{UID: fx.User["uid"], Email: fx.User["email"], Name: fx.User["name"]}
	type parsed struct {
		Content   string          `json:"content"`
		Checklist []ChecklistItem `json:"checklist"`
	}
	inputs := append(append([]string{}, fx.Parse...), manyItems(fx.ManyItems))
	goOut := map[string]any{}
	var parses []parsed
	for _, text := range inputs {
		c, items := ParseChecklist(text)
		if items == nil {
			items = []ChecklistItem{}
		}
		parses = append(parses, parsed{c, items})
	}
	goOut["parse"] = parses
	var created []Note
	for _, c := range fx.NewNote {
		if c.Parse != "" {
			content, items := ParseChecklist(c.Parse)
			created = append(created, NewNote(user, content, c.Date, items, now))
		} else {
			created = append(created, NewNote(user, c.Content, c.Date, nil, now))
		}
	}
	goOut["new_note"] = created
	var patches []Patch
	for _, c := range fx.Patch {
		patches = append(patches, PatchFor(c.Note, changesFrom(t, c.Changes), now))
	}
	goOut["patch"] = patches
	var emails [][]string
	for _, l := range fx.Emails {
		emails = append(emails, NormalizeEmails(l))
	}
	goOut["emails"] = emails

	want, got := normalize(t, jsOut), normalize(t, mustJSON(t, goOut))
	for _, section := range []string{"parse", "new_note", "patch", "emails"} {
		w, g := want[section].([]any), got[section].([]any)
		if len(w) != len(g) {
			t.Errorf("%s: %d JS results, %d Go results", section, len(w), len(g))
			continue
		}
		for i := range w {
			if !reflect.DeepEqual(w[i], g[i]) {
				t.Errorf("%s[%d] differs\n  JS: %s\n  Go: %s", section, i, mustJSON(t, w[i]), mustJSON(t, g[i]))
			}
		}
	}
}

func manyItems(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "- item " + itoa(i)
	}
	return strings.Join(lines, "\n")
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

// changesFrom turns a fixture's changes object into Changes, keeping "present vs absent" exactly.
func changesFrom(t *testing.T, raw map[string]json.RawMessage) Changes {
	t.Helper()
	var c Changes
	decode := func(key string, into any) bool {
		v, ok := raw[key]
		if ok {
			if err := json.Unmarshal(v, into); err != nil {
				t.Fatalf("fixture %s: %v", key, err)
			}
		}
		return ok
	}
	var s1, s2, s3 string
	var f float64
	var b bool
	var items []ChecklistItem
	if decode("content", &s1) {
		c.Content = &s1
	}
	if decode("status", &s2) {
		c.Status = &s2
	}
	if decode("note_date", &s3) {
		c.NoteDate = &s3
	}
	if decode("order", &f) {
		c.Order = &f
	}
	if decode("deleted", &b) {
		c.Deleted = &b
	}
	if decode("checklist", &items) {
		c.Checklist = &items
	}
	return c
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// normalize decodes JSON generically and blanks generated checklist ids in parse/new_note
// results (the only values the spec allows to differ). The note "id" field exists only in Go
// (Firestore keeps it outside the document), so it is dropped from new_note results.
func normalize(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	blankIDs := func(m map[string]any) {
		if items, ok := m["checklist"].([]any); ok {
			for _, it := range items {
				it.(map[string]any)["id"] = "<generated>"
			}
		}
	}
	for _, p := range v["parse"].([]any) {
		blankIDs(p.(map[string]any))
	}
	for _, n := range v["new_note"].([]any) {
		m := n.(map[string]any)
		delete(m, "id")
		blankIDs(m)
	}
	return v
}
