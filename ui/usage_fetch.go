// Package ui — usage_fetch.go makes the exact same request the Usage
// Tracker extension makes (GET /organizations/<org>/usage), but from this
// Go process directly, using session cookies decrypted straight off that
// instance's own disk (see usage_cookies_windows.go / usage_cookies_other.go
// for readSessionCookies). No running claude.exe is required — this is what
// lets the picker show live numbers for every instance the moment it opens.
//
// Every failure path here calls logUsage (usage_log.go) before returning
// ok=false, so a failed read is never silent — check
// %APPDATA%\ClaudeWebExtLauncher\usage-debug.log.
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
// session (401/403), no network, or a response we don't recognize. See
// usage-debug.log for which of these it was.
func fetchLiveUsageDirect(instanceName string) (fh, sd int, ok bool) {
	sessionKey, orgID, found := readSessionCookies(instanceName)
	if !found || sessionKey == "" || orgID == "" {
		logUsage(instanceName, "fetch: no usable session cookies (found=%v sessionKey_empty=%v orgID_empty=%v)",
			found, sessionKey == "", orgID == "")
		return 0, 0, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), usageFetchTimeout)
	defer cancel()

	url := "https://claude.ai/api/organizations/" + orgID + "/usage"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		logUsage(instanceName, "fetch: NewRequestWithContext failed: %v", err)
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
		logUsage(instanceName, "fetch: request to %s failed: %v", url, err)
		return 0, 0, false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB cap, response is tiny JSON
	if err != nil {
		logUsage(instanceName, "fetch: reading response body failed: %v", err)
		return 0, 0, false
	}
	if resp.StatusCode != http.StatusOK {
		// Include a truncated body snippet — this is almost always where
		// the real answer lives (401 "unauthorized", org not found, etc.)
		snippet := body
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		logUsage(instanceName, "fetch: HTTP %d from %s, body: %s", resp.StatusCode, url, snippet)
		return 0, 0, false
	}

	fh, sd, ok = parseUsagePercentages(body)
	if !ok {
		snippet := body
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		logUsage(instanceName, "fetch: HTTP 200 but response didn't match either known shape, body: %s", snippet)
		return 0, 0, false
	}
	logUsage(instanceName, "fetch: OK fh=%d sd=%d", fh, sd)
	return fh, sd, true
}
