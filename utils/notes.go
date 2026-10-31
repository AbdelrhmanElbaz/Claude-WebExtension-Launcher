package utils

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Note is an instance's notes: a short one shown in the instance list, and a longer
// Markdown one.
type Note struct {
	Quick     string `json:"quick,omitempty"`
	Full      string `json:"full,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"` // Unix milliseconds
}

// notes.json sits next to settings.json, keyed by the name the instance list shows.
// It's kept apart from the instances' own data folders on purpose: those belong to
// Claude and get deleted with the instance, reinstalled or cleared.
var notesMu sync.Mutex

func notesPath() string {
	return filepath.Join(logDir(), "notes.json")
}

func loadNotes() map[string]Note {
	notes := map[string]Note{}
	if data, err := os.ReadFile(notesPath()); err == nil {
		json.Unmarshal(data, &notes)
	}
	if notes == nil {
		notes = map[string]Note{}
	}
	return notes
}

func saveNotes(notes map[string]Note) error {
	data, err := json.MarshalIndent(notes, "", "  ")
	if err != nil {
		return err
	}
	path := notesPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return WriteFileAtomic(path, data, 0600)
}

// GetNote returns an instance's notes (empty if it has none).
func GetNote(instance string) Note {
	notesMu.Lock()
	defer notesMu.Unlock()
	return loadNotes()[instance]
}

// SetNote stores an instance's notes; two empty notes remove the entry.
func SetNote(instance, quick, full string) error {
	notesMu.Lock()
	defer notesMu.Unlock()
	notes := loadNotes()
	if quick == "" && full == "" {
		delete(notes, instance)
	} else {
		notes[instance] = Note{Quick: quick, Full: full, UpdatedAt: time.Now().UnixMilli()}
	}
	return saveNotes(notes)
}

// DeleteNote forgets an instance's notes.
func DeleteNote(instance string) error {
	return SetNote(instance, "", "")
}
