// Package ui — usage.go reads each instance's plan-usage-history.json
// (written by Claude Desktop itself, NOT by us) to surface two numbers in
// the picker: the 5-hour rolling usage % ("fh") and the weekly/session
// usage % ("sd"), plus a "last active" timestamp taken from the newest
// sample in the file.
//
// File shape on disk (%APPDATA%\Claude-<instance>\plan-usage-history.json):
//
//	{
//	  "version": 2,
//	  "samples": [
//	    {"t": 1785573914850, "org": "...", "u": {"fh": 75, "sd": 9}},
//	    {"t": 1787978774321, "org": "...", "u": {}}
//	  ]
//	}
//
// "u" is often `{}` (no active limits tracked at that moment — e.g. between
// sessions, or on a plan without visible rate limits). We always take the
// LAST sample for "last active", but we take the last sample whose "u" is
// non-empty for the usage percentages, since a trailing run of empty `{}`
// samples shouldn't blank out the last known real reading.
package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// InstanceUsage is what the UI needs to render one instance's usage row.
type InstanceUsage struct {
	// FiveHourPct / WeeklyPct are 0-100. HasUsage is false if we never found
	// a sample with non-empty "u" (e.g. brand new instance, never launched,
	// or a plan that doesn't report limits) — in that case the UI should
	// show "—" instead of a 0% bar, since 0% is a real, different state.
	FiveHourPct int
	WeeklyPct   int
	HasUsage    bool

	// LastActive is the timestamp of the newest sample in the file,
	// regardless of whether it carried usage numbers. Zero value means we
	// have no data at all (file missing / instance never launched).
	LastActive time.Time
	HasActivity bool
}

type usageSample struct {
	T   int64 `json:"t"` // epoch milliseconds
	Org string `json:"org"`
	U   struct {
		FH int `json:"fh"`
		SD int `json:"sd"`
	} `json:"u"`
}

type usageFile struct {
	Version int           `json:"version"`
	Samples []usageSample `json:"samples"`
}

// hasUsageData reports whether a decoded sample actually carried "fh"/"sd"
// keys, vs. an empty `{}` object (which decodes to the zero struct too —
// so we re-check the raw JSON instead of trusting the typed struct alone).
func sampleHasUsage(raw json.RawMessage) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	return len(m) > 0
}

// rawUsageFile mirrors usageFile but keeps "u" as raw JSON so we can tell
// `{}` apart from `{"fh":0,"sd":0}` — both are legitimate, different states.
type rawUsageSample struct {
	T   int64           `json:"t"`
	Org string          `json:"org"`
	U   json.RawMessage `json:"u"`
}

type rawUsageFile struct {
	Version int              `json:"version"`
	Samples []rawUsageSample `json:"samples"`
}

// instanceUserDataDir mirrors claudeUserDataDir() in main_windows.go — kept
// as a small local copy so this file has no import-cycle risk; if that
// helper is ever exported from a shared package, swap this out for it.
func instanceUserDataDir(instance string) string {
	return filepath.Join(os.Getenv("APPDATA"), "Claude-"+instance)
}

// ReadInstanceUsage loads and parses plan-usage-history.json for one
// instance. A missing file (instance never launched) is not an error — it
// just yields a zero-value InstanceUsage with both Has* flags false.
func ReadInstanceUsage(instanceName string) InstanceUsage {
	var out InstanceUsage

	p := filepath.Join(instanceUserDataDir(instanceName), "plan-usage-history.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return out // missing/unreadable — render as "no data" in the UI
	}

	var f rawUsageFile
	if err := json.Unmarshal(data, &f); err != nil || len(f.Samples) == 0 {
		return out
	}

	// Last sample overall -> "last active", walking backwards.
	last := f.Samples[len(f.Samples)-1]
	out.LastActive = time.UnixMilli(last.T)
	out.HasActivity = true

	// Walk backwards for the most recent sample with non-empty "u".
	for i := len(f.Samples) - 1; i >= 0; i-- {
		s := f.Samples[i]
		if !sampleHasUsage(s.U) {
			continue
		}
		var u struct {
			FH int `json:"fh"`
			SD int `json:"sd"`
		}
		if err := json.Unmarshal(s.U, &u); err != nil {
			continue
		}
		out.FiveHourPct = u.FH
		out.WeeklyPct = u.SD
		out.HasUsage = true
		break
	}

	return out
}

// RelativeTime renders a friendly "منذ ٣ ساعات"-style string. Kept in
// English tokens here; picker.go is responsible for localizing the copy
// around this if/when the UI is translated — this just does the bucketing.
func RelativeTime(t time.Time) string {
	if t.IsZero() {
		return "Never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "Just now"
	case d < time.Hour:
		m := int(d.Minutes())
		return pluralize(m, "minute") + " ago"
	case d < 24*time.Hour:
		h := int(d.Hours())
		return pluralize(h, "hour") + " ago"
	case d < 7*24*time.Hour:
		days := int(d.Hours() / 24)
		return pluralize(days, "day") + " ago"
	default:
		return t.Format("Jan 2, 2006")
	}
}

func pluralize(n int, unit string) string {
	s := unit
	if n != 1 {
		s += "s"
	}
	return itoa(n) + " " + s
}

func itoa(n int) string {
	// Avoid pulling in strconv just for this — tiny helper, ints only.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
