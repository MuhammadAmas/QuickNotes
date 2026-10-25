package tools

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/MuhammadAmas/QuickNotes/mcp/internal/notes"
)

// idPattern matches note and checklist item ids the apps create (Firestore auto ids, UUIDs,
// "local-<uid>-<n>"). Rejecting anything else also keeps ids from changing Firestore paths.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

func validateID(arg, id string) error {
	if id == "" {
		return fmt.Errorf("%s is required", arg)
	}
	if !idPattern.MatchString(id) {
		return fmt.Errorf("%s %q is not a valid id; use an id returned by list_notes or search_notes", arg, id)
	}
	return nil
}

// resolveDate returns value, or fallback when value is empty, after checking it is a real
// YYYY-MM-DD date.
func resolveDate(arg, value, fallback string) (string, error) {
	if value == "" {
		return fallback, nil
	}
	if _, ok := notes.ParseDate(value); !ok {
		return "", fmt.Errorf("%s must be a real date in YYYY-MM-DD form, got %q", arg, value)
	}
	return value, nil
}

func validateChoice(arg, value string, allowed []string) error {
	if value == "" || slices.Contains(allowed, value) {
		return nil
	}
	return fmt.Errorf("%s must be one of %s, got %q", arg, strings.Join(allowed, ", "), value)
}

// resolveLimit applies a default to 0 and checks the range.
func resolveLimit(arg string, value, def, max int) (int, error) {
	if value == 0 {
		return def, nil
	}
	if value < 1 || value > max {
		return 0, fmt.Errorf("%s must be between 1 and %d, got %d", arg, max, value)
	}
	return value, nil
}

// validateText checks a text argument's length in the apps' units after trimming.
func validateText(arg, value string, max int) (string, error) {
	value = notes.TrimJS(value)
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", arg)
	}
	if n := notes.LenUTF16(value); n > max {
		return "", fmt.Errorf("%s is too long: %d characters, the limit is %d", arg, n, max)
	}
	return value, nil
}
