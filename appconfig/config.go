// Package appconfig persists the launcher's own settings (list of known
// instances, whether to show the picker on startup, last used instance,
// and misc toggles that used to be CLI-only flags).
package appconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config is the on-disk shape of launcher-config.json.
type Config struct {
	// Instances is the list of instance names the picker knows about.
	// The user can add to this via the "+" card in the UI; it is also
	// auto-populated the first time an instance is launched via --instance.
	Instances []string `json:"instances"`

	// LastUsedInstance is preselected / auto-launched depending on ShowPickerOnStartup.
	LastUsedInstance string `json:"last_used_instance"`

	// ShowPickerOnStartup mirrors Chrome's "Show on startup" checkbox.
	// true  -> always show the instance picker window.
	// false -> skip the picker and launch LastUsedInstance directly.
	ShowPickerOnStartup bool `json:"show_picker_on_startup"`

	// Settings that used to be plain CLI flags, now editable from the Settings page.
	ForceUpdate bool `json:"force_update"`
	DebugMode   bool `json:"debug_mode"`
	AutoUpdate  bool `json:"auto_update"` // maps to skipping selfupdate.CheckAndUpdate when false

	// SkipClaudeUpdateCheck disables ensureClaudeReady's version check/patch
	// step entirely (the one that triggers the UAC "administrator privileges
	// required for patching" prompt on Windows). This is separate from
	// AutoUpdate, which only controls the *launcher's own* self-update check.
	// When true, Claude is launched as-is with no patch/update attempt.
	SkipClaudeUpdateCheck bool `json:"skip_claude_update_check"`
}

// Default returns the config used the first time the app ever runs.
func Default() *Config {
	return &Config{
		Instances:           []string{"modified"},
		LastUsedInstance:    "modified",
		ShowPickerOnStartup: true,
		ForceUpdate:           false,
		DebugMode:             false,
		AutoUpdate:            true,
		SkipClaudeUpdateCheck: false,
	}
}

// path returns the location of launcher-config.json, next to the executable
// so the config travels with portable installs.
func path() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exePath), "launcher-config.json"), nil
}

// Load reads launcher-config.json, creating it with defaults if missing.
func Load() (*Config, error) {
	p, err := path()
	if err != nil {
		return Default(), err
	}

	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		cfg := Default()
		_ = cfg.Save() // best-effort; picker will still work even if this fails
		return cfg, nil
	}
	if err != nil {
		return Default(), err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), err
	}
	if len(cfg.Instances) == 0 {
		cfg.Instances = []string{"modified"}
	}
	return &cfg, nil
}

// Save writes the config back to disk.
func (c *Config) Save() error {
	p, err := path()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0644)
}

// AddInstance registers a new instance name if it isn't already known.
func (c *Config) AddInstance(name string) {
	for _, existing := range c.Instances {
		if existing == name {
			return
		}
	}
	c.Instances = append(c.Instances, name)
}

// RemoveInstance drops an instance from the known list (does not delete its
// data directory on disk - that is a separate, explicit action in the UI).
func (c *Config) RemoveInstance(name string) {
	out := c.Instances[:0]
	for _, existing := range c.Instances {
		if existing != name {
			out = append(out, existing)
		}
	}
	c.Instances = out
	if c.LastUsedInstance == name && len(c.Instances) > 0 {
		c.LastUsedInstance = c.Instances[0]
	}
}
