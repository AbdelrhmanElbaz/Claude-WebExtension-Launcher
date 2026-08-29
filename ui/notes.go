// Package ui — notes.go owns per-instance notes: a short "quick note"
// (plain multiline text, edited inline on the card) and a longer
// "full note" (Markdown, edited in its own window with a live preview and
// manual RTL/LTR direction control).
//
// Both notes live in a single file next to launcher-config.json:
//
//	launcher-notes.json
//	{
//	  "Claude-Main": {
//	    "quickNote": "...",
//	    "fullNote": "# ...\n...",
//	    "updatedAt": 1788007399618
//	  }
//	}
//
// Rationale for keeping this separate from each instance's own AppData
// folder: that folder belongs to Claude Desktop itself and can be touched
// by updates/reinstalls/cache clears. Keeping notes next to the launcher
// binary means one place to back up and one place that survives Claude
// Desktop being reinstalled.
package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// noteEntry is the on-disk shape for one instance's notes.
type noteEntry struct {
	QuickNote string `json:"quickNote"`
	FullNote  string `json:"fullNote"`
	UpdatedAt int64  `json:"updatedAt"` // epoch ms
}

// notesStore is the whole launcher-notes.json file, keyed by instance name.
type notesStore map[string]noteEntry

func notesPath() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exePath), "launcher-notes.json"), nil
}

// loadNotes reads launcher-notes.json, returning an empty store (not an
// error) if the file doesn't exist yet — brand new install, no notes taken.
func loadNotes() notesStore {
	p, err := notesPath()
	if err != nil {
		return notesStore{}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return notesStore{}
	}
	var s notesStore
	if err := json.Unmarshal(data, &s); err != nil {
		return notesStore{}
	}
	return s
}

func (s notesStore) save() error {
	p, err := notesPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0644)
}

func (s notesStore) get(instance string) noteEntry {
	return s[instance] // zero value if absent — fine, empty notes.
}

func (s notesStore) setQuickNote(instance, text string) {
	e := s[instance]
	e.QuickNote = text
	e.UpdatedAt = time.Now().UnixMilli()
	s[instance] = e
}

func (s notesStore) setFullNote(instance, text string) {
	e := s[instance]
	e.FullNote = text
	e.UpdatedAt = time.Now().UnixMilli()
	s[instance] = e
}

// ---------------------------------------------------------------------
// RTL / LTR direction toggling.
//
// Fyne's text widgets don't expose a per-paragraph "direction" property,
// so we do what browsers and every other bidi-aware plain-text editor do
// under the hood: insert Unicode directional formatting characters.
//
//   RLE (U+202B) ... PDF (U+202C)  -> force a whole block right-to-left
//   LRE (U+202A) ... PDF (U+202C)  -> force a whole block left-to-right
//   RLM (U+200F) at line start     -> nudge a single line RTL
//   LRM (U+200E) at line start     -> nudge a single line LTR
//
// "Toggle whole document" (Ctrl+` / Ctrl+ذ) strips any existing whole-text
// wrapper and applies the opposite one.
// "Toggle current line" (Ctrl+\ / Ctrl+|) only touches the line the caret
// is on, by prefixing/flipping its leading mark.
// ---------------------------------------------------------------------

const (
	rle = '\u202B' // Right-to-Left Embedding
	lre = '\u202A' // Left-to-Right Embedding
	pdf = '\u202C' // Pop Directional Formatting
	rlm = '\u200F' // Right-to-Left Mark
	lrm = '\u200E' // Left-to-Right Mark
)

// toggleWholeTextDirection flips the RTL/LTR embedding of the entire
// string. If the text is already wrapped in RLE/PDF or LRE/PDF, it strips
// that wrapper and applies the opposite one; otherwise it wraps fresh
// (defaulting to "currently LTR -> switch to RTL").
func toggleWholeTextDirection(text string) string {
	r := []rune(text)
	if len(r) >= 2 && r[len(r)-1] == pdf && (r[0] == rle || r[0] == lre) {
		wasRTL := r[0] == rle
		inner := string(r[1 : len(r)-1])
		if wasRTL {
			return string(lre) + inner + string(pdf)
		}
		return string(rle) + inner + string(pdf)
	}
	// No wrapper yet — assume it reads LTR today, so switch to RTL.
	return string(rle) + text + string(pdf)
}

// toggleLineDirection flips just the line the caret is currently on
// (identified by caretLine, 0-indexed) by inserting/flipping a leading
// RLM/LRM mark on that line only.
func toggleLineDirection(text string, caretLine int) string {
	lines := strings.Split(text, "\n")
	if caretLine < 0 || caretLine >= len(lines) {
		return text
	}
	line := lines[caretLine]
	r := []rune(line)
	switch {
	case len(r) > 0 && r[0] == rlm:
		lines[caretLine] = string(lrm) + string(r[1:]) // was forced RTL -> force LTR
	case len(r) > 0 && r[0] == lrm:
		lines[caretLine] = string(rlm) + string(r[1:])
	default:
		lines[caretLine] = string(rlm) + line // default: nudge to RTL first
	}
	return strings.Join(lines, "\n")
}

// caretLineFromCursorRow adapts Fyne's CursorRow (already 0-indexed line
// number for widget.Entry) — kept as a named pass-through so the call site
// in ShowFullNoteEditor reads clearly.
func caretLineFromCursorRow(row int) int { return row }

// utf16Len exists only so contributors aren't tempted to "optimize" the
// rune-based helpers above into byte-index math — direction marks must be
// counted in runes/UTF-16 code units, never raw bytes, or multi-byte
// Arabic/Latin text gets corrupted on toggle. (Unused directly; documents
// the constraint for future edits.)
var _ = utf16.Encode

// ---------------------------------------------------------------------
// UI
// ---------------------------------------------------------------------

// ShowFullNoteEditor opens the Markdown editor/preview window for one
// instance's long-form note and saves back to notesStore on close.
func ShowFullNoteEditor(a fyne.App, instanceName string) {
	store := loadNotes()
	entry := store.get(instanceName)

	w := a.NewWindow("Notes — " + instanceName)
	w.Resize(fyne.NewSize(760, 560))

	editor := widget.NewMultiLineEntry()
	editor.SetText(entry.FullNote)
	editor.Wrapping = fyne.TextWrapWord

	preview := widget.NewRichTextFromMarkdown(entry.FullNote)
	preview.Wrapping = fyne.TextWrapWord

	previewScroll := container.NewVScroll(preview)
	editorScroll := container.NewVScroll(editor)

	split := container.NewHSplit(editorScroll, previewScroll)
	split.Offset = 0.5

	syncPreview := func() { preview.ParseMarkdown(editor.Text) }
	editor.OnChanged = func(string) { syncPreview() }

	status := widget.NewLabel("Ctrl+` toggles the whole note's direction · Ctrl+\\ toggles the current line")
	status.TextStyle = fyne.TextStyle{Italic: true}

	saveAndClose := func() {
		store = loadNotes() // reload in case another window saved meanwhile
		store.setFullNote(instanceName, editor.Text)
		_ = store.save()
		w.Close()
	}

	saveBtn := widget.NewButton("Save", saveAndClose)
	closeBtn := widget.NewButton("Cancel", func() { w.Close() })
	buttons := container.NewBorder(nil, nil, nil, container.NewHBox(closeBtn, saveBtn))

	// --- Direction shortcuts -------------------------------------------------
	// Whole-document toggle: Ctrl+` (KeyGraveAccent covers the physical key
	// that types ذ on Arabic keyboard layouts too, since it's a *physical*
	// key binding, not a rune binding).
	wholeDirShortcut := &desktop.CustomShortcut{KeyName: fyne.KeyBackTick, Modifier: fyne.KeyModifierControl}
	w.Canvas().AddShortcut(wholeDirShortcut, func(fyne.Shortcut) {
		editor.SetText(toggleWholeTextDirection(editor.Text))
		syncPreview()
	})

	// Per-line toggle: Ctrl+\ (physical key also types | with Shift, and
	// covers the ص/\ position some AR layouts use — again bound by physical
	// key, not by rune, so Shift state doesn't matter).
	lineDirShortcut := &desktop.CustomShortcut{KeyName: fyne.KeyBackslash, Modifier: fyne.KeyModifierControl}
	w.Canvas().AddShortcut(lineDirShortcut, func(fyne.Shortcut) {
		editor.SetText(toggleLineDirection(editor.Text, caretLineFromCursorRow(editor.CursorRow)))
		syncPreview()
	})

	content := container.NewBorder(nil, container.NewVBox(status, buttons), nil, nil, split)
	w.SetContent(container.NewPadded(content))
	w.SetCloseIntercept(saveAndClose)
	w.Show()
}

// ---------------------------------------------------------------------
// Quick note (inline, on the card)
// ---------------------------------------------------------------------

// makeQuickNoteEntry builds the small multiline entry used directly on an
// instance card, auto-saving (debounced by Fyne's own OnChanged batching)
// as the user types.
func makeQuickNoteEntry(instanceName string, initial string) *widget.Entry {
	e := widget.NewMultiLineEntry()
	e.SetText(initial)
	e.Wrapping = fyne.TextWrapWord
	e.SetMinRowsVisible(2)
	e.PlaceHolder = "Quick note…"
	e.OnChanged = func(text string) {
		store := loadNotes()
		store.setQuickNote(instanceName, text)
		_ = store.save()
	}
	return e
}

// sortedInstanceNames is a tiny helper kept here (rather than in picker.go)
// since it's only used when rendering notes-adjacent UI in a stable order.
func sortedInstanceNames(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}
