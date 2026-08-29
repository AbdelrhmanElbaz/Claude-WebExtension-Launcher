// Package ui — usage_storage.go reads the *real* live source of the usage
// numbers: chrome.storage.local, as written directly by the Usage Tracker
// extension. This replaces our old assumption that plan-usage-history.json
// was that source — it isn't (see usage.go's header comment history). That
// file is written by Claude Desktop's own Settings→Usage feature, a
// completely separate mechanism from the extension, and it started lagging/
// breaking for everyone around Aug 22 2026 for reasons outside our control.
//
// Electron persists chrome.storage.local for an extension as a LevelDB
// database on disk at:
//
//	%APPDATA%\Claude-<instance>\Local Extension Settings\<extension-id>\
//
// "<extension-id>" is NOT something we control or can read out of the
// extension's manifest.json — extensions loaded unpacked (no "key" field)
// get an ID Chromium *derives* from the absolute install path. We reproduce
// that derivation in chromiumExtensionID() below so we don't have to guess
// or scan for the right folder.
//
// The DB directory is exclusively locked (via a "LOCK" file) for as long as
// the instance's Electron process has it open, so we can't leveldb.OpenFile
// it directly while Claude Desktop is running. We work around this the
// standard way: copy the DB's files to a scratch temp dir (skipping "LOCK",
// which we don't need — we're not writing) and open the copy instead.
package ui

import (
	"claude-webext-patcher/utils"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

// usageTrackerFolder must match the Folder used for the lugia19 extension in
// extensions/extensions.go — kept as a separate constant here (rather than
// importing that package) to avoid a dependency between ui and extensions.
const usageTrackerFolder = "usage-tracker"

// chromiumExtensionID reproduces crx_file::id_util::GenerateId, which is how
// Chromium assigns a stable ID to an unpacked extension with no "key" in its
// manifest.json: SHA-256 the extension's absolute install path, take the
// first 16 bytes, hex-encode them, then map each hex digit to a letter
// (0->a, 1->b, ... 15->p). Electron's session.extensions.loadExtension is
// backed by the same Chromium extensions system, so this applies unchanged.
//
// On Windows, Chromium lower-cases the path before hashing
// (MaybeNormalizePath); on other platforms it hashes it as-is.
func chromiumExtensionID(installPath string) string {
	p := installPath
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	sum := sha256.Sum256([]byte(p))
	hexStr := hex.EncodeToString(sum[:16]) // first 16 bytes -> 32 hex chars

	var b strings.Builder
	b.Grow(32)
	for _, c := range hexStr {
		var d byte
		if c >= '0' && c <= '9' {
			d = byte(c - '0')
		} else { // 'a'-'f'
			d = byte(c-'a') + 10
		}
		b.WriteByte('a' + d)
	}
	return b.String()
}

// usageExtensionID is computed once — the extension's install path is fixed
// per-machine (utils.ResolveInstallPath is a constant on Windows, and the
// web-extensions folder is shared across all instances; only the *userData*
// under %APPDATA%\Claude-<instance> differs per instance).
var usageExtensionID = chromiumExtensionID(
	filepath.Join(utils.ResolveInstallPath("web-extensions"), usageTrackerFolder),
)

// extensionStorageDir returns the (possibly-locked) LevelDB directory for
// one instance's copy of the Usage Tracker extension's chrome.storage.local.
func extensionStorageDir(instanceName string) string {
	return filepath.Join(
		instanceUserDataDir(instanceName),
		"Local Extension Settings",
		usageExtensionID,
	)
}

// copyLevelDBDir snapshots a LevelDB directory into a fresh temp dir so we
// can open it read-only without fighting the live process for its LOCK
// file. LevelDB directories are flat (no subdirectories), and individual
// .ldb/.log/MANIFEST files aren't exclusively locked on Windows or macOS —
// only LOCK is — so a plain file-by-file copy is safe to do concurrently.
// Returns the temp dir and a cleanup func; caller must always call cleanup.
func copyLevelDBDir(src string) (dir string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "claudewebext-usage-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() { os.RemoveAll(tmp) }

	entries, err := os.ReadDir(src)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}

	copied := 0
	for _, e := range entries {
		if e.IsDir() || e.Name() == "LOCK" {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(src, e.Name()))
		if readErr != nil {
			// A .log/.ldb file mid-write, or a transient sharing-violation
			// on Windows — skip it and keep going with what we can get.
			continue
		}
		if os.WriteFile(filepath.Join(tmp, e.Name()), data, 0o644) == nil {
			copied++
		}
	}
	if copied == 0 {
		cleanup()
		return "", func() {}, os.ErrNotExist
	}
	return tmp, cleanup, nil
}

// findUsageRecordInDB scans every key/value pair in an already-copied
// LevelDB directory and returns the first value that parses into our
// expected shape ({"version":..,"samples":[{"t":..,"u":{"fh":..,"sd":..}}]}).
// We deliberately don't hardcode the chrome.storage.local KEY the extension
// uses internally — that's an implementation detail of lugia19's extension,
// isn't part of any public contract, and could rename across versions. We
// only rely on the shape of the value, which is what actually feeds the
// live 5h/7d numbers shown inside Claude itself.
func findUsageRecordInDB(dbDir string) (rawUsageFile, bool) {
	db, err := leveldb.OpenFile(dbDir, &opt.Options{ErrorIfMissing: true})
	if err != nil {
		return rawUsageFile{}, false
	}
	defer db.Close()

	iter := db.NewIterator(nil, nil)
	defer iter.Release()

	for iter.Next() {
		raw := iter.Value()

		var f rawUsageFile
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		if len(f.Samples) == 0 {
			continue
		}
		// A real sample always has a nonzero epoch-ms timestamp — cheap
		// sanity check against matching some unrelated JSON blob that
		// happens to also have a "samples" field.
		if f.Samples[len(f.Samples)-1].T == 0 {
			continue
		}
		return f, true
	}
	return rawUsageFile{}, false
}

// readUsageFromExtensionStorage is the new primary usage source. It returns
// ok=false whenever anything along the way is missing (extension never ran
// for this instance, DB not created yet, shape not recognized, etc.) so the
// caller can fall back to the legacy plan-usage-history.json reader.
func readUsageFromExtensionStorage(instanceName string) (rawUsageFile, bool) {
	dbDir := extensionStorageDir(instanceName)
	if _, err := os.Stat(dbDir); err != nil {
		return rawUsageFile{}, false
	}

	tmp, cleanup, err := copyLevelDBDir(dbDir)
	if err != nil {
		return rawUsageFile{}, false
	}
	defer cleanup()

	return findUsageRecordInDB(tmp)
}
