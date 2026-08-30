//go:build windows

// Package ui — usage_cookies_windows.go reads this instance's own claude.ai
// session directly off disk, the same way any Chromium-based app's cookies
// are protected on Windows, so the Go UI can call the /usage API itself
// without needing that instance's claude.exe to be running at all.
//
// Two pieces of Chromium-standard on-disk state are involved:
//
//  1. `<userData>\Local State` (plain JSON) holds the AES key used to
//     encrypt every cookie value, itself encrypted with Windows DPAPI:
//     os_crypt.encrypted_key = base64("DPAPI" + CryptProtectData(aesKey)).
//     CryptUnprotectData (called here via crypt32.dll — the standard, fully
//     documented Win32 API for this, no reverse engineering involved) undoes
//     that outer layer and gives us the raw AES-256 key.
//  2. `<userData>\Network\Cookies` is a SQLite database; each row's
//     `encrypted_value` column is "v10"/"v20" + 12-byte GCM nonce +
//     AES-256-GCM ciphertext, encrypted with that same key.
//
// This mirrors exactly what Chrome/Electron do internally — it's how every
// desktop password/cookie manager that reads Chromium data works — just
// done from a separate Go process instead of from inside Electron.
package ui

import (
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	_ "modernc.org/sqlite"
)

var (
	modcrypt32             = syscall.NewLazyDLL("crypt32.dll")
	modkernel32            = syscall.NewLazyDLL("kernel32.dll")
	procCryptUnprotectData = modcrypt32.NewProc("CryptUnprotectData")
	procLocalFree          = modkernel32.NewProc("LocalFree")
)

// dataBlob mirrors Win32's DATA_BLOB struct layout exactly (cbData then
// pbData), which is what CryptUnprotectData expects as in/out parameters.
type dataBlob struct {
	cbData uint32
	pbData *byte
}

// dpapiUnprotect wraps CryptUnprotectData. On any non-Windows-DPAPI-capable
// context (rare — e.g. a domain-joined machine with no user profile key
// available yet) this simply fails and the caller falls back cleanly.
func dpapiUnprotect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("dpapi: empty input")
	}
	in := dataBlob{cbData: uint32(len(data)), pbData: &data[0]}
	var out dataBlob

	r, _, callErr := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData failed: %w", callErr)
	}
	// out.pbData was allocated by Windows (LocalAlloc under the hood) — it's
	// ours to free, and we must copy its contents out before freeing it.
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))

	result := make([]byte, out.cbData)
	copy(result, unsafe.Slice(out.pbData, out.cbData))
	return result, nil
}

type localStateFile struct {
	OSCrypt struct {
		EncryptedKey string `json:"encrypted_key"`
	} `json:"os_crypt"`
}

// loadMasterKey extracts and decrypts one instance's cookie-encryption key
// from its own Local State file.
func loadMasterKey(instanceName string) ([]byte, error) {
	p := filepath.Join(instanceUserDataDir(instanceName), "Local State")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}

	var ls localStateFile
	if err := json.Unmarshal(data, &ls); err != nil {
		return nil, err
	}
	if ls.OSCrypt.EncryptedKey == "" {
		return nil, errors.New("no os_crypt.encrypted_key in Local State")
	}

	raw, err := base64.StdEncoding.DecodeString(ls.OSCrypt.EncryptedKey)
	if err != nil {
		return nil, err
	}

	const dpapiMarker = "DPAPI"
	if len(raw) <= len(dpapiMarker) || string(raw[:len(dpapiMarker)]) != dpapiMarker {
		return nil, errors.New("encrypted_key missing expected DPAPI marker")
	}
	return dpapiUnprotect(raw[len(dpapiMarker):])
}

// decryptCookieValue undoes Chromium's per-cookie AES-256-GCM encryption.
// Cookies written before any encryption existed (or already plaintext for
// some other reason) have no v10/v20 prefix — returned as-is in that case.
// isCleanCookieText reports whether s looks like a real Chromium cookie
// value: printable ASCII only, no control/binary bytes. A cookie that
// fails this check means something upstream of the crypto (WAL
// consistency, wrong row, wrong offset) handed us bad bytes even though
// GCM's own tag check passed — GCM authenticates whatever bytes it was
// given, it can't tell us those bytes were the wrong row's data.
func isCleanCookieText(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// hexPreview renders up to n bytes of s as hex, for logging garbage values
// without dumping an entire (possibly sensitive) cookie into the log file.
func hexPreview(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return fmt.Sprintf("% x", s)
}

func decryptCookieValue(masterKey, encrypted []byte) (string, error) {
	const prefixLen = 3
	const nonceLen = 12

	if len(encrypted) < prefixLen {
		return string(encrypted), nil
	}
	prefix := string(encrypted[:prefixLen])
	if prefix != "v10" && prefix != "v20" {
		return string(encrypted), nil
	}
	if len(encrypted) < prefixLen+nonceLen+1 {
		return "", errors.New("cookie ciphertext too short")
	}

	nonce := encrypted[prefixLen : prefixLen+nonceLen]
	ciphertext := encrypted[prefixLen+nonceLen:]

	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("AES-GCM decrypt failed: %w", err)
	}
	return string(plain), nil
}

// copySingleFile snapshots one file into a fresh temp file, the same
// lock-avoidance trick used for LevelDB earlier: Network\Cookies is a live
// SQLite database that may be open (in WAL/journal mode) by a running
// claude.exe, but a plain byte-for-byte copy doesn't need an exclusive lock.
//
// Retries a handful of times on failure: an actively-used profile can hold
// a brief exclusive lock while Chromium checkpoints the WAL, and that
// window is usually gone within a few hundred ms.
func copySingleFile(src string) (path string, cleanup func(), err error) {
	const maxAttempts = 5
	const retryDelay = 150 * time.Millisecond

	var data []byte
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		data, err = os.ReadFile(src)
		if err == nil {
			break
		}
		if attempt < maxAttempts {
			time.Sleep(retryDelay)
		}
	}
	if err != nil {
		return "", func() {}, fmt.Errorf("read %s after %d attempts: %w", src, maxAttempts, err)
	}

	tmp, err := os.CreateTemp("", "claudewebext-cookies-*.sqlite")
	if err != nil {
		return "", func() {}, err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	cleanup = func() { os.Remove(tmpPath) }

	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		cleanup()
		return "", func() {}, err
	}

	// The main DB file alone is not a consistent snapshot while SQLite is
	// running in WAL mode (the default for Chromium's Cookies DB): recent
	// row changes live in the "-wal" sidecar until the next checkpoint,
	// and reading the main file without it can hand the driver
	// inconsistent page data — which showed up here as `encrypted_value`
	// blobs that decrypted "successfully" (valid GCM tag) but with extra
	// garbage bytes, since the value read was assembled from stale/
	// overlapping page content rather than the actual current row. Copy
	// the WAL and its "-shm" shared-memory index alongside the main file,
	// best-effort — if a given instance was fully checkpointed and has no
	// WAL file at all, that's a normal, harmless case, not an error.
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar := src + suffix
		sidecarData, serr := os.ReadFile(sidecar)
		if serr != nil {
			continue // no WAL/SHM right now — fine, main file alone is consistent
		}
		_ = os.WriteFile(tmpPath+suffix, sidecarData, 0o600)
	}
	oldCleanup := cleanup
	cleanup = func() {
		oldCleanup()
		os.Remove(tmpPath + "-wal")
		os.Remove(tmpPath + "-shm")
	}

	return tmpPath, cleanup, nil
}

// readEncryptedCookies pulls the two raw (still-encrypted) cookie values we
// need out of one instance's Cookies SQLite database.
func readEncryptedCookies(instanceName string) (sessionKeyEnc, orgIDEnc []byte, err error) {
	cookiesPath := filepath.Join(instanceUserDataDir(instanceName), "Network", "Cookies")
	tmpPath, cleanup, err := copySingleFile(cookiesPath)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()

	db, err := sql.Open("sqlite", tmpPath)
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		`SELECT name, encrypted_value FROM cookies
		 WHERE host_key LIKE '%claude.ai' AND name IN ('sessionKey', 'lastActiveOrg')`,
	)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var val []byte
		if err := rows.Scan(&name, &val); err != nil {
			continue
		}
		switch name {
		case "sessionKey":
			sessionKeyEnc = val
		case "lastActiveOrg":
			orgIDEnc = val
		}
	}
	return sessionKeyEnc, orgIDEnc, rows.Err()
}

// readSessionCookies is the public entry point used by usage_fetch.go: it
// returns this instance's decrypted sessionKey + active org ID, or
// ok=false if the instance has never logged in (or anything along the way
// couldn't be read — a locked file we somehow still couldn't copy, a
// missing DPAPI key, etc.).
func readSessionCookies(instanceName string) (sessionKey, orgID string, ok bool) {
	masterKey, err := loadMasterKey(instanceName)
	if err != nil {
		logUsage(instanceName, "cookies: loadMasterKey failed: %v", err)
		return "", "", false
	}

	sessEnc, orgEnc, err := readEncryptedCookies(instanceName)
	if err != nil {
		logUsage(instanceName, "cookies: readEncryptedCookies failed: %v", err)
		return "", "", false
	}
	if len(sessEnc) == 0 || len(orgEnc) == 0 {
		logUsage(instanceName, "cookies: missing cookie row(s) (sessionKey_present=%v lastActiveOrg_present=%v) — instance likely never logged in",
			len(sessEnc) != 0, len(orgEnc) != 0)
		return "", "", false
	}

	sess, err := decryptCookieValue(masterKey, sessEnc)
	if err != nil {
		logUsage(instanceName, "cookies: decrypting sessionKey failed: %v", err)
		return "", "", false
	}
	if !isCleanCookieText(sess) {
		logUsage(instanceName, "cookies: sessionKey decrypted to non-text garbage (len=%d, first bytes: %s) — likely a stale/inconsistent DB read, not a real cookie value",
			len(sess), hexPreview(sess, 24))
		return "", "", false
	}
	org, err := decryptCookieValue(masterKey, orgEnc)
	if err != nil {
		logUsage(instanceName, "cookies: decrypting lastActiveOrg failed: %v", err)
		return "", "", false
	}
	if !isCleanCookieText(org) {
		logUsage(instanceName, "cookies: lastActiveOrg decrypted to non-text garbage (len=%d, first bytes: %s) — likely a stale/inconsistent DB read, not a real cookie value",
			len(org), hexPreview(org, 24))
		return "", "", false
	}
	return sess, org, true
}
