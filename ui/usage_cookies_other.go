//go:build !windows

package ui

// readSessionCookies isn't implemented on this platform yet — macOS
// Chromium apps encrypt cookies with a Keychain-derived key (PBKDF2 +
// AES-128-CBC), a different scheme from Windows DPAPI + AES-256-GCM, and
// this launcher's usage UI has only been built out for Windows so far.
// Returning ok=false here just means callers fall back to
// plan-usage-history.json, same as before this feature existed.
func readSessionCookies(instanceName string) (sessionKey, orgID string, ok bool) {
	return "", "", false
}
