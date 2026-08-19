package patcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	originalHash = "0682c5f2076f099c34cfdd15a9e063849ed437a49677e6fcc5b4198c76575be5"
	replacedHash = "31b716965a03d8b37bb9e75471e52d8f2b759ba1f6aa4d3a045762fdfd17cfc4"
)

// samplePlist mirrors the shape of Claude.app's Info.plist: the integrity dict sits
// among other keys, and CFBundleIdentifier follows it.
func samplePlist(hash string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>Claude</string>
	<key>ElectronAsarIntegrity</key>
	<dict>
		<key>Resources/app.asar</key>
		<dict>
			<key>algorithm</key>
			<string>SHA256</string>
			<key>hash</key>
			<string>` + hash + `</string>
		</dict>
	</dict>
	<key>CFBundleIdentifier</key>
	<string>com.anthropic.claudefordesktop</string>
</dict>
</plist>
`
}

func writeTempPlist(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Info.plist")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadWritePlistAsarHash(t *testing.T) {
	before := samplePlist(originalHash)
	path := writeTempPlist(t, before)

	got, err := readPlistAsarHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != originalHash {
		t.Fatalf("readPlistAsarHash = %s, want %s", got, originalHash)
	}

	if err := writePlistAsarHash(path, replacedHash); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The edit is length-preserving, so nothing else in the file may move.
	if len(after) != len(before) {
		t.Errorf("file length changed: %d -> %d", len(before), len(after))
	}
	if want := samplePlist(replacedHash); string(after) != want {
		t.Errorf("plist after write:\n%s\nwant:\n%s", after, want)
	}

	got, err = readPlistAsarHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != replacedHash {
		t.Errorf("hash after write = %s, want %s", got, replacedHash)
	}
}

// TestPlistAsarHashUnrelatedDigest guards the reason we anchor on the surrounding
// markup: an identical digest elsewhere in the file must not be mistaken for ours.
func TestPlistAsarHashUnrelatedDigest(t *testing.T) {
	content := strings.Replace(samplePlist(originalHash),
		"\t<key>CFBundleIdentifier</key>",
		"\t<key>SomeOtherDigest</key>\n\t<string>"+originalHash+"</string>\n\t<key>CFBundleIdentifier</key>", 1)
	path := writeTempPlist(t, content)

	if err := writePlistAsarHash(path, replacedHash); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(after), replacedHash) != 1 {
		t.Error("expected exactly one occurrence of the new hash")
	}
	if !strings.Contains(string(after), "<key>SomeOtherDigest</key>\n\t<string>"+originalHash+"</string>") {
		t.Error("unrelated digest was modified")
	}
}

// TestPlistAsarHashNotFound covers the cases that must hand off to the PlistBuddy
// fallback (missing key, binary plist) versus the ones that are outright errors.
func TestPlistAsarHashNotFound(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"key absent", `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>CFBundleName</key><string>Claude</string></dict></plist>`},
		{"integrity dict without hash", `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
	<key>Resources/app.asar</key>
	<dict><key>algorithm</key><string>SHA256</string></dict>
</dict></plist>`},
		{"binary plist", "bplist00\x00\x01\x02\x03"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempPlist(t, tc.content)

			_, err := readPlistAsarHash(path)
			if !errors.Is(err, errPlistHashNotFound) {
				t.Errorf("read: got %v, want errPlistHashNotFound", err)
			}
			if err := writePlistAsarHash(path, replacedHash); !errors.Is(err, errPlistHashNotFound) {
				t.Errorf("write: got %v, want errPlistHashNotFound", err)
			}
		})
	}
}

// TestWritePlistAsarHashRejectsBadHash is the guard issue #38 asked for: the malformed
// "<hex>, N bytes" value must never reach Info.plist.
func TestWritePlistAsarHashRejectsBadHash(t *testing.T) {
	path := writeTempPlist(t, samplePlist(originalHash))

	for _, bad := range []string{
		replacedHash + ", 70665 bytes",
		"",
		strings.ToUpper(replacedHash),
		replacedHash[:63],
		strings.Repeat("z", 64),
	} {
		if err := writePlistAsarHash(path, bad); err == nil {
			t.Errorf("expected an error writing %q", bad)
		}
	}

	got, err := readPlistAsarHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != originalHash {
		t.Errorf("plist was modified by a rejected write: %s", got)
	}
}

func TestIsHexHash(t *testing.T) {
	valid := []string{replacedHash, strings.Repeat("0", 64), strings.Repeat("f", 64)}
	for _, s := range valid {
		if !isHexHash(s) {
			t.Errorf("isHexHash(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"",
		replacedHash[:63],
		replacedHash + "0",
		replacedHash + ", 70665 bytes",
		strings.ToUpper(replacedHash),
		strings.Repeat("g", 64),
	}
	for _, s := range invalid {
		if isHexHash(s) {
			t.Errorf("isHexHash(%q) = true, want false", s)
		}
	}
}
