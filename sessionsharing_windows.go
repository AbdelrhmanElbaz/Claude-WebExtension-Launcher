//go:build windows

package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// sharedSessionFolders are the userData subfolders that hold self-contained session
// data and should be shared between the official and patched Claude installs. The
// install-specific folders (claude-code = the CLI binary, claude-code-vm = VM state)
// are deliberately NOT shared.
var sharedSessionFolders = []string{
	"local-agent-mode-sessions", // Cowork / agent-mode sessions
	"claude-code-sessions",      // Claude Code sessions
}

// SetupSessionSharing makes the official and patched Claude installs read/write a single
// physical copy of their Cowork and Code sessions, via Windows directory junctions that
// point into a neutral, launcher-owned canonical store. This avoids any two-way sync (and
// the deletion-tracking problem that comes with it): there is only ever one copy.
//
// The canonical store lives at %APPDATA%\ClaudeWebExtLauncher\shared-sessions so it
// survives uninstalling either app. On first run, existing sessions from both installs are
// merged into the store (newer-mtime wins) and each original folder is backed up before
// being replaced by a junction. Runs unelevated and is fully idempotent.
func SetupSessionSharing(instanceName string) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return
	}

	neutralRoot := filepath.Join(appData, "ClaudeWebExtLauncher", "shared-sessions")

	// Always share the patched instance; share the official install too if present.
	installRoots := []string{filepath.Join(appData, "Claude-"+instanceName)}
	officialRoot := filepath.Join(appData, "Claude")
	if info, err := os.Stat(officialRoot); err == nil && info.IsDir() {
		installRoots = append(installRoots, officialRoot)
	}

	fmt.Println("Setting up shared Code/Cowork sessions...")
	for _, folder := range sharedSessionFolders {
		canonical := filepath.Join(neutralRoot, folder)
		if err := os.MkdirAll(canonical, 0755); err != nil {
			fmt.Printf("Warning: could not create shared store %s: %v\n", canonical, err)
			continue
		}
		for _, root := range installRoots {
			link := filepath.Join(root, folder)
			if err := ensureJunction(link, canonical); err != nil {
				fmt.Printf("Warning: could not share %s: %v\n", link, err)
			}
		}
	}
}

// CleanupOfficialJunctions removes the session junctions under %APPDATA%\Claude (the link
// only, never the target). Called before uninstalling the official MSIX so the uninstaller
// can't follow a junction into the canonical store and delete the shared sessions.
func CleanupOfficialJunctions() {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return
	}
	officialRoot := filepath.Join(appData, "Claude")
	for _, folder := range sharedSessionFolders {
		link := filepath.Join(officialRoot, folder)
		if _, err := os.Readlink(link); err != nil {
			continue // not a junction (or doesn't exist) — leave it alone
		}
		// os.Remove on a directory junction removes only the reparse point.
		if err := os.Remove(link); err != nil {
			fmt.Printf("Warning: could not remove junction %s: %v\n", link, err)
		}
	}
}

// ensureJunction makes link a directory junction pointing at canonical, idempotently.
// If link is already the correct junction it's a no-op; if it's a stale junction it's
// recreated; if it's a real directory its contents are merged into canonical, backed up,
// and replaced by the junction.
func ensureJunction(link, canonical string) error {
	// Already a reparse point we can resolve?
	if target, err := os.Readlink(link); err == nil {
		if samePath(target, canonical) {
			return nil
		}
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("remove stale junction: %w", err)
		}
		return makeJunction(link, canonical)
	}

	info, err := os.Lstat(link)
	if err != nil {
		// Doesn't exist — just create the junction.
		return makeJunction(link, canonical)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		// A reparse point we couldn't read via Readlink; recreate it.
		os.Remove(link)
		return makeJunction(link, canonical)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s exists and is not a directory", link)
	}

	// Real directory with data: merge into the canonical store first.
	if err := mergeTree(link, canonical); err != nil {
		return fmt.Errorf("merge into shared store: %w", err)
	}
	// Back up the original once; if a backup already exists the data is safely in the
	// canonical store, so just drop the real folder.
	backup := link + ".presync-backup"
	if _, err := os.Stat(backup); err == nil {
		if err := os.RemoveAll(link); err != nil {
			return fmt.Errorf("remove merged folder: %w", err)
		}
	} else if err := os.Rename(link, backup); err != nil {
		return fmt.Errorf("back up original folder: %w", err)
	}
	return makeJunction(link, canonical)
}

// makeJunction creates a directory junction (link -> target) via mklink /J, which needs
// no admin privileges. The link's parent is created if missing.
func makeJunction(link, target string) error {
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		return err
	}
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mklink /J %q %q failed: %v\n%s", link, target, err, out)
	}
	return nil
}

// mergeTree recursively copies src into dst, overwriting a destination file only when the
// source file's modification time is newer (so a re-run never clobbers fresher data).
// File mtimes are preserved so the newer-wins comparison stays stable across runs.
func mergeTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		srcInfo, err := d.Info()
		if err != nil {
			return err
		}
		if dstInfo, err := os.Stat(target); err == nil {
			if !srcInfo.ModTime().After(dstInfo.ModTime()) {
				return nil // destination is same-or-newer — keep it
			}
		}
		return copyFile(path, target, srcInfo.ModTime())
	})
}

// copyFile copies src to dst (creating parent dirs) and restores src's modification time.
func copyFile(src, dst string, mtime time.Time) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, mtime, mtime)
}

// samePath reports whether two paths refer to the same location (case-insensitive on
// Windows, after cleaning and resolving to absolute form).
func samePath(a, b string) bool {
	ca, err1 := filepath.Abs(a)
	cb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return strings.EqualFold(ca, cb)
}
