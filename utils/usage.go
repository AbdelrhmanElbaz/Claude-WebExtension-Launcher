package utils

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Usage is an instance's last known 5-hour and weekly usage, as written by the
// launcher's wrapper.js (injected into Claude) whenever Claude reads its usage. A nil
// percentage means that limit wasn't reported.
type Usage struct {
	FiveHour  *float64 `json:"fiveHour"`
	Weekly    *float64 `json:"weekly"`
	UpdatedAt int64    `json:"updatedAt"` // Unix milliseconds
}

// Age is how long ago the reading was taken.
func (u Usage) Age() time.Duration {
	return time.Since(time.UnixMilli(u.UpdatedAt))
}

// ReadUsage returns the last usage snapshot of an instance (by the name the list shows
// it under); ok is false if there isn't a readable one.
func ReadUsage(instance string) (u Usage, ok bool) {
	data, err := os.ReadFile(filepath.Join(logDir(), "usage", instance+".json"))
	if err != nil || json.Unmarshal(data, &u) != nil {
		return Usage{}, false
	}
	return u, u.FiveHour != nil || u.Weekly != nil
}

// DeleteUsage forgets an instance's usage snapshot.
func DeleteUsage(instance string) {
	os.Remove(filepath.Join(logDir(), "usage", instance+".json"))
}
