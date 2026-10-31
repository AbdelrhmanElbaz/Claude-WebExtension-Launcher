package main

// Extras for the instance list: usage, last opened, notes, and the options that skip the
// update checks. The list itself is in instances.go, the window in gui/.

import (
	"fmt"
	"strings"
	"time"

	"claude-webext-patcher/utils"
)

// usageStaleAfter is how old a usage reading can be before the list says so: it's only
// refreshed while that instance is running.
const usageStaleAfter = 30 * time.Minute

// listName is the name an instance has in the list, and so in its notes, usage and
// last-opened time (the main instance may be launched under its legacy name).
func listName(instance string) string {
	if strings.EqualFold(instance, mainInstanceName) || strings.EqualFold(instance, legacyMainInstanceName) {
		return mainInstanceName
	}
	return instance
}

// recordLastOpened notes that an instance is being launched now.
func recordLastOpened(instance string) {
	name := listName(instance)
	err := utils.UpdateSettings(func(s *utils.Settings) {
		if s.LastOpened == nil {
			s.LastOpened = map[string]int64{}
		}
		s.LastOpened[name] = time.Now().UnixMilli()
	})
	if err != nil {
		fmt.Printf("Warning: could not save the last opened time: %v\n", err)
	}
}

// agoText is a rough "3 hours ago".
func agoText(d time.Duration) string {
	plural := func(n int, unit string) string {
		if n == 1 {
			return fmt.Sprintf("1 %s ago", unit)
		}
		return fmt.Sprintf("%d %ss ago", n, unit)
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	default:
		return plural(int(d.Hours()/24), "day")
	}
}

func usageText(name string) string {
	u, ok := utils.ReadUsage(name)
	if !ok {
		return "Usage: –"
	}
	pct := func(label string, v *float64) string {
		if v == nil {
			return label + " –"
		}
		return fmt.Sprintf("%s %.0f%%", label, *v)
	}
	text := "Usage: " + pct("5h", u.FiveHour) + " · " + pct("7d", u.Weekly)
	if u.Age() > usageStaleAfter {
		text += " (" + agoText(u.Age()) + ")"
	}
	return text
}

func lastOpenedText(ms int64) string {
	if ms == 0 {
		return "Last opened: never"
	}
	return "Last opened: " + agoText(time.Since(time.UnixMilli(ms)))
}

// instanceDetails is the dim line under an instance's name in the list.
func instanceDetails(name string) string {
	s := utils.LoadSettings()
	var parts []string
	if !s.HideUsage {
		parts = append(parts, usageText(name))
	}
	if !s.HideLastOpened {
		parts = append(parts, lastOpenedText(s.LastOpened[name]))
	}
	return strings.Join(parts, "  •  ")
}
