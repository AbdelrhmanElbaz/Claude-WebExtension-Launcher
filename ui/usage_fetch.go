// Package ui — usage_fetch.go now reads the small JSON snapshot the
// Usage Tracker extension itself exports (via the same console-bridge
// wrapper.js already uses for CUT_ALARM/CUT_NOTIFICATION), instead of
// decrypting that instance's session cookies off disk and calling the
// /usage endpoint directly.
//
// Why the change: claude.ai cookies are now protected by Chrome's
// App-Bound Encryption, so a naive DPAPI/AES-GCM decrypt of sessionKey
// no longer yields the real token. Properly unwrapping an App-Bound
// -encrypted value requires driving Chromium's elevation service — the
// same mechanism credential-stealing malware uses to defeat that
// protection — so this project doesn't do that. Instead, the extension
// (already running inside an authenticated claude.ai tab) reads its own
// already-computed usage numbers and hands them to Node voluntarily;
// wrapper.js writes them to
// %APPDATA%\ClaudeWebExtLauncher\usage\<instance>.json, and this file
// just reads that back. No cookies are read or decrypted anywhere in
// this package anymore.
//
// Every failure path here calls logUsage (usage_log.go) before
// returning ok=false, so a failed read is never silent — check
// %APPDATA%\ClaudeWebExtLauncher\usage-debug.log.
package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// usageSnapshotMaxAge bounds how old an exported snapshot can be before
// we treat it as stale (instance not currently running / extension not
// syncing) rather than a live reading.
const usageSnapshotMaxAge = 30 * time.Minute

type usageLimit struct {
	Percentage int   `json:"percentage"`
	ResetsAt   int64 `json:"resetsAt"` // epoch milliseconds, as exported by the extension
}

type usageDataJSON struct {
	Limits struct {
		Session      *usageLimit `json:"session"`      // five_hour
		Weekly       *usageLimit `json:"weekly"`       // seven_day
		SonnetWeekly *usageLimit `json:"sonnetWeekly"` // seven_day_sonnet (Max only)
		OpusWeekly   *usageLimit `json:"opusWeekly"`   // seven_day_opus (Max only)
	} `json:"limits"`
	OrgId string `json:"orgId"`
}

type usageExportFile struct {
	Instance  string        `json:"instance"`
	UpdatedAt int64         `json:"updatedAt"`
	UsageData usageDataJSON `json:"usageData"`
}

func usageExportDir() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", fmt.Errorf("%%APPDATA%% not set")
	}
	return filepath.Join(appData, "ClaudeWebExtLauncher", "usage"), nil
}

// fetchLiveUsageDirect keeps its original name and signature so
// usage.go (ReadInstanceUsage) needs no changes — only the
// implementation (and the source of truth) changed.
func fetchLiveUsageDirect(instanceName string) (fh, sd int, ok bool) {
	dir, err := usageExportDir()
	if err != nil {
		logUsage(instanceName, "fetch: %v", err)
		return 0, 0, false
	}

	path := filepath.Join(dir, instanceName+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			logUsage(instanceName, "fetch: no usage export yet at %s (instance hasn't run since the extension update, or hasn't synced yet)", path)
		} else {
			logUsage(instanceName, "fetch: reading %s failed: %v", path, err)
		}
		return 0, 0, false
	}

	var snap usageExportFile
	if err := json.Unmarshal(data, &snap); err != nil {
		logUsage(instanceName, "fetch: corrupt usage export at %s: %v", path, err)
		return 0, 0, false
	}

	age := time.Since(time.UnixMilli(snap.UpdatedAt))
	if age > usageSnapshotMaxAge {
		logUsage(instanceName, "fetch: usage export at %s is stale (%s old), instance likely not running", path, age.Round(time.Minute))
		return 0, 0, false
	}

	session := snap.UsageData.Limits.Session
	weekly := snap.UsageData.Limits.Weekly
	if session == nil && weekly == nil {
		logUsage(instanceName, "fetch: usage export present but both session and weekly limits are null (never logged in / free plan?)")
		return 0, 0, false
	}

	if session != nil {
		fh = session.Percentage
	}
	if weekly != nil {
		sd = weekly.Percentage
	}
	logUsage(instanceName, "fetch: OK (from extension export) fh=%d sd=%d", fh, sd)
	return fh, sd, true
}
