// Package ui — usage_fetch.go makes the exact same request the Usage
// Tracker extension makes (GET /organizations/<org>/usage), but from this
// Go process directly, using session cookies decrypted straight off that
// instance's own disk (see usage_cookies_windows.go / usage_cookies_other.go
// for readSessionCookies). No running claude.exe is required — this is what
// lets the picker show live numbers for every instance the moment it opens.
package ui

import (
	"context"
	"io"
	"net/http"
	"time"
)

// usageFetchTimeout bounds each instance's request so one slow/unreachable
// account can't stall the whole picker; see FetchAllUsage in picker.go for
// how these run concurrently across instances.
const usageFetchTimeout = 5 * time.Second

// fetchLiveUsageDirect returns ok=false for every "nothing to show" case:
// never logged in (no sessionKey/lastActiveOrg cookie yet), an expired
// session (401/403), no network, or a response we don't recognize.
func fetchLiveUsageDirect(instanceName string) (fh, sd int, ok bool) {
	sessionKey, orgID, found := readSessionCookies(instanceName)
	if !found || sessionKey == "" || orgID == "" {
		return 0, 0, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), usageFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://claude.ai/api/organizations/"+orgID+"/usage", nil)
	if err != nil {
		return 0, 0, false
	}

	// Node's bare http client, and Go's for that matter, looks nothing like a
	// browser request (no UA, no Origin/Referer) and gets rejected by
	// Anthropic's edge protection even with a perfectly valid session
	// cookie — add the headers a normal claude.ai page/extension request
	// would carry alongside it.
	req.Header.Set("Cookie", "sessionKey="+sessionKey+"; lastActiveOrg="+orgID)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Electron/32.0.0 Safari/537.36")
	req.Header.Set("Origin", "https://claude.ai")
	req.Header.Set("Referer", "https://claude.ai/")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, 0, false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB cap, response is tiny JSON
	if err != nil || resp.StatusCode != http.StatusOK {
		return 0, 0, false
	}

	return parseUsagePercentages(body)
}
