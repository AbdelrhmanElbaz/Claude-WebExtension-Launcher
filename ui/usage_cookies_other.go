//go:build !windows

package ui

// readSessionCookies isn't implemented on this platform yet — macOS
// Chromium apps encrypt cookies with a Keychain-derived key (PBKDF2 +
// AES-128-CBC), a different scheme from Windows DPAPI + AES-256-GCM, and
// this launcher's usage UI has only been built out for Windows so far.
// Returning ok=false here means ReadInstanceUsage's live fetch always
// misses on this platform, so the picker shows "—" for usage % (Last
// Active still works, since that comes from plan-usage-history.json
// regardless of platform).
func readSessionCookies(instanceName string) (sessionKey, orgID string, ok bool) {
	logUsage(instanceName, "cookies: readSessionCookies not implemented on this platform")
	return "", "", false
}
