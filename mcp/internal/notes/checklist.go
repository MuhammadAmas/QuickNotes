package notes

import (
	"regexp"
	"strings"
)

// jsSpace is the set of characters JavaScript's \s and String.trim() treat as whitespace.
// Go's \s only covers ASCII, so the ported patterns spell this set out to behave the same.
const jsSpace = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// jsAny matches what JavaScript's "." matches: anything but a line terminator.
const jsAny = `[^\n\r\x{2028}\x{2029}]`

// listLine is LIST_LINE from notes-model.js:
// "- milk", "* milk", "• milk", "[ ] milk", "[x] milk", "- [x] milk".
var listLine = regexp.MustCompile(
	`^[` + jsSpace + `]*(?:[-*•][` + jsSpace + `]*)?\[[` + jsSpace + `]*([xX✓]?)[` + jsSpace + `]*\][` + jsSpace + `]+(` + jsAny + `+)$` +
		`|^[` + jsSpace + `]*[-*•][` + jsSpace + `]+(` + jsAny + `+)$`)

// TrimJS removes leading and trailing whitespace exactly like JavaScript's String.trim().
func TrimJS(s string) string { return strings.TrimFunc(s, isJSSpace) }

func isJSSpace(r rune) bool {
	return strings.ContainsRune("\t\n\v\f\r \u00a0\u1680\u2028\u2029\u202f\u205f\u3000\ufeff", r) || (r >= 0x2000 && r <= 0x200a)
}

// ParseChecklist splits typed text into the note's text and its checklist, like parseChecklist()
// in notes-model.js: lines that look like list items become checklist items, the other non-blank
// lines stay as the note text. Without list lines the trimmed text is returned unchanged.
func ParseChecklist(text string) (content string, checklist []ChecklistItem) {
	var rest []string
	for _, line := range strings.Split(text, "\n") {
		if m := listLine.FindStringSubmatch(line); m != nil {
			itemText := m[2]
			if itemText == "" {
				itemText = m[3]
			}
			checklist = append(checklist, NewChecklistItem(TrimJS(itemText), m[1] != ""))
		} else if TrimJS(line) != "" {
			rest = append(rest, TrimJS(line))
		}
	}
	if len(checklist) == 0 {
		return TrimJS(text), nil
	}
	content = strings.Join(rest, "\n")
	if content == "" {
		content = "Checklist"
	}
	if len(checklist) > MaxChecklist {
		checklist = checklist[:MaxChecklist]
	}
	return content, checklist
}
