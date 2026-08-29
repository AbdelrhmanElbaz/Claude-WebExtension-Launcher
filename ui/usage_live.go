// Package ui — usage_live.go reads usage-live.json, a small file wrapper.js
// (resources/injections/generic/wrapper.js) writes on its own, from inside
// the Electron main process, using that instance's own claude.ai cookies.
//
// Why this file exists instead of reading something the extension writes:
// the Usage Tracker extension does NOT persist the live 5h/7d percentages
// anywhere on disk. Confirmed by reading its actual source
// (bg-components/claude-api.js): getUsageData() always makes a fresh
// GET /organizations/<org>/usage call and hands the result straight to the
// content script over a runtime message — there is no cache file, no
// LevelDB record, nothing to scrape. So wrapper.js makes that exact same
// request itself: it reads the same two cookies the extension reads
// (`lastActiveOrg`, `sessionKey` — see content_utils.js:getActiveOrgId and
// bg-components/container-strategy.js) and hits the same endpoint, then
// writes the raw JSON response here for us to read from a separate process.
//
// File shape written by wrapper.js (%APPDATA%\Claude-<instance>\usage-live.json):
//
//	{
//	  "fetchedAt": 1787978774321,   // epoch ms, when wrapper.js made the request
//	  "orgId": "...",
//	  "usage": { ... raw /usage API response, either shape below ... }
//	}
//
// The /usage response itself comes in two shapes (mirrors
// shared/dataclasses.js UsageData.fromAPIResponse in the extension):
//
//	New (preferred when present): {"limits":[{"kind":"session","percent":81,"resets_at":"..."}, {"kind":"weekly_all","percent":7,"resets_at":"..."}]}
//	Old (fallback):                {"five_hour":{"utilization":81,"resets_at":"..."}, "seven_day":{"utilization":7,"resets_at":"..."}}
package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const usageLiveFileName = "usage-live.json"

type liveUsageFile struct {
	FetchedAt int64           `json:"fetchedAt"`
	OrgID     string          `json:"orgId"`
	Usage     json.RawMessage `json:"usage"`
}

type usageLimitEntryNew struct {
	Kind     string  `json:"kind"`
	Percent  float64 `json:"percent"`
	ResetsAt string  `json:"resets_at"`
}

type usageAPIResponseNew struct {
	Limits []usageLimitEntryNew `json:"limits"`
}

type usageLimitOld struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type usageAPIResponseOld struct {
	FiveHour *usageLimitOld `json:"five_hour"`
	SevenDay *usageLimitOld `json:"seven_day"`
}

// parseUsagePercentages mirrors UsageData.fromAPIResponse: prefer the new
// `limits` array when it's present and non-empty, otherwise fall back to
// the old top-level `five_hour`/`seven_day` fields. Returns ok=false if
// neither shape yields a session ("fh") reading at all.
func parseUsagePercentages(raw json.RawMessage) (fh, sd int, ok bool) {
	var newResp usageAPIResponseNew
	if json.Unmarshal(raw, &newResp) == nil && len(newResp.Limits) > 0 {
		found := false
		for _, l := range newResp.Limits {
			switch l.Kind {
			case "session":
				fh = int(l.Percent + 0.5)
				found = true
			case "weekly_all":
				sd = int(l.Percent + 0.5)
				found = true
			}
		}
		if found {
			return fh, sd, true
		}
	}

	var oldResp usageAPIResponseOld
	if json.Unmarshal(raw, &oldResp) == nil && oldResp.FiveHour != nil {
		fh = int(oldResp.FiveHour.Utilization + 0.5)
		if oldResp.SevenDay != nil {
			sd = int(oldResp.SevenDay.Utilization + 0.5)
		}
		return fh, sd, true
	}

	return 0, 0, false
}

// readLiveUsageFile reads and parses one instance's usage-live.json.
// ok=false covers every "nothing to show yet" case: wrapper.js hasn't
// written it yet (instance never launched, or launched but not logged in
// yet), the file is stale JSON from a crash mid-write, or the /usage
// response wrapper.js captured was itself an error body.
func readLiveUsageFile(instanceName string) (fetchedAt int64, fh, sd int, ok bool) {
	p := filepath.Join(instanceUserDataDir(instanceName), usageLiveFileName)
	data, err := os.ReadFile(p)
	if err != nil {
		return 0, 0, 0, false
	}

	var f liveUsageFile
	if err := json.Unmarshal(data, &f); err != nil || len(f.Usage) == 0 {
		return 0, 0, 0, false
	}

	fh, sd, ok = parseUsagePercentages(f.Usage)
	if !ok {
		return 0, 0, 0, false
	}
	return f.FetchedAt, fh, sd, true
}
