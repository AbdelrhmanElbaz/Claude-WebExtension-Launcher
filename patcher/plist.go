package patcher

import (
	"errors"
	"fmt"
	"os"
	"regexp"
)

// errPlistHashNotFound means the Info.plist isn't in the XML layout we know how to
// splice — either Claude moved the key, or the bundle now ships a binary plist. It is
// the signal for the macOS caller to fall back to PlistBuddy rather than give up.
var errPlistHashNotFound = errors.New("ElectronAsarIntegrity hash for Resources/app.asar not found in Info.plist")

// asarHashRe matches the app.asar integrity hash inside an XML Info.plist:
//
//	<key>Resources/app.asar</key>
//	<dict>
//		<key>algorithm</key>
//		<string>SHA256</string>
//		<key>hash</key>
//		<string>0123...</string>
//	</dict>
//
// It anchors on the surrounding markup rather than the bare hex so it can't match the
// same digest somewhere else in the file, and the span between the two keys is bounded
// so a missing <key>hash</key> can't let the match run on into a later dict.
var asarHashRe = regexp.MustCompile(
	`(?s)<key>Resources/app\.asar</key>\s*<dict>.{0,400}?<key>hash</key>\s*<string>([0-9a-fA-F]{64})</string>`)

// isHexHash reports whether s is a 64-character lowercase hex digest — the shape
// Electron's integrity check requires. Issue #38 was a malformed value ("<hex>, N
// bytes") reaching Info.plist unchecked, so the write path validates before it writes.
func isHexHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// locatePlistAsarHash returns the byte span of the integrity hash value within data.
// Anything other than exactly one match is an error: zero means the layout changed,
// more than one means we can't tell which is authoritative.
func locatePlistAsarHash(data []byte) (start, end int, err error) {
	matches := asarHashRe.FindAllSubmatchIndex(data, 2)
	if len(matches) == 0 {
		return 0, 0, errPlistHashNotFound
	}
	if len(matches) > 1 {
		return 0, 0, fmt.Errorf("found multiple ElectronAsarIntegrity hash entries in Info.plist, expected exactly one")
	}
	return matches[0][2], matches[0][3], nil
}

// readPlistAsarHash returns the integrity hash currently recorded in the XML plist at
// plistPath.
func readPlistAsarHash(plistPath string) (string, error) {
	data, err := os.ReadFile(plistPath)
	if err != nil {
		return "", fmt.Errorf("reading %s: %v", plistPath, err)
	}
	start, end, err := locatePlistAsarHash(data)
	if err != nil {
		return "", err
	}
	return string(data[start:end]), nil
}

// writePlistAsarHash splices newHash over the existing one. Both values are 64 hex
// characters, so the edit is length-preserving and nothing else in the file moves.
func writePlistAsarHash(plistPath, newHash string) error {
	if !isHexHash(newHash) {
		return fmt.Errorf("refusing to write %q to Info.plist: not a 64-character lowercase hex digest", newHash)
	}

	data, err := os.ReadFile(plistPath)
	if err != nil {
		return fmt.Errorf("reading %s: %v", plistPath, err)
	}
	start, end, err := locatePlistAsarHash(data)
	if err != nil {
		return err
	}
	if end-start != len(newHash) {
		return fmt.Errorf("Info.plist hash is %d bytes, expected %d", end-start, len(newHash))
	}

	updated := append([]byte(nil), data...)
	copy(updated[start:end], newHash)

	if err := os.WriteFile(plistPath, updated, 0644); err != nil {
		return fmt.Errorf("writing %s: %v", plistPath, err)
	}
	return nil
}
