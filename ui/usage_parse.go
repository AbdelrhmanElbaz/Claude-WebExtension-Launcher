// Package ui — usage_parse.go parses Anthropic's /usage API response, in
// either shape it comes in (mirrors shared/dataclasses.js's
// UsageData.fromAPIResponse in the Usage Tracker extension's own source —
// confirmed by reading it directly, not guessed):
//
//	New (preferred when present): {"limits":[{"kind":"session","percent":81,"resets_at":"..."}, {"kind":"weekly_all","percent":7,"resets_at":"..."}]}
//	Old (fallback):                {"five_hour":{"utilization":81,"resets_at":"..."}, "seven_day":{"utilization":7,"resets_at":"..."}}
//
// See usage_fetch.go for what calls this.
package ui

import "encoding/json"

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
// neither shape yields a session ("fh") reading at all — which is also what
// happens if raw is actually an error body (missing org, expired session,
// etc.) rather than a real usage response.
func parseUsagePercentages(raw []byte) (fh, sd int, ok bool) {
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
