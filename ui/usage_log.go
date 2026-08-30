// Package ui — usage_log.go gives usage_fetch.go / usage_cookies_windows.go
// a single place to record *why* a live usage read failed, since every
// failure path in those files previously just returned ok=false with no
// trace at all ("fails silently" — see usage.go's package comment history).
//
// Writes go to %APPDATA%\ClaudeWebExtLauncher\usage-debug.log, appended,
// one line per event, so a user can zip/send this one file for support
// without needing to run anything from a terminal. Logging failures here
// (disk full, permissions, etc.) are swallowed on purpose — this is a
// diagnostics aid, it must never be the thing that breaks the picker.
package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var usageLogMu sync.Mutex

func usageLogPath() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", fmt.Errorf("APPDATA not set")
	}
	dir := filepath.Join(appData, "ClaudeWebExtLauncher")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "usage-debug.log"), nil
}

// logUsage appends one timestamped, instance-tagged line. instanceName may
// be "" for messages not tied to a specific instance.
func logUsage(instanceName, format string, args ...interface{}) {
	usageLogMu.Lock()
	defer usageLogMu.Unlock()

	p, err := usageLogPath()
	if err != nil {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	tag := instanceName
	if tag == "" {
		tag = "-"
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(f, "%s [%s] %s\n", time.Now().Format("2006-01-02 15:04:05.000"), tag, msg)
}
