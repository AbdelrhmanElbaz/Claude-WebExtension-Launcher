package main

import (
	"claude-webext-patcher/appconfig"
	"claude-webext-patcher/patcher"
	"claude-webext-patcher/selfupdate"
	"claude-webext-patcher/ui"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

var launchClaudeInTerminal = false

// Version is the current version of the application
const Version = "3.3.3"

// defaultInstanceName is the instance used when --instance is not given. Only this
// instance shares Cowork/Code sessions with the official install; named instances stay
// isolated (see SetupSessionSharing / RepairSessionSharing).
const defaultInstanceName = "modified"

func main() {
	// Parse command-line flags. Flags still work for scripting/shortcuts and
	// always override whatever is in launcher-config.json / the picker.
	forceUpdateFlag := flag.Bool("force-update", false, "Force update to the latest version even if it's not verified compatible")
	instanceFlag := flag.String("instance", "", "Instance name for separate data directory and lock (skips the picker window)")
	patcherMode := flag.Bool("patcher", false, "Run in elevated patcher mode (internal)")
	debugFlag := flag.Bool("debug", false, "Keep console windows open and launch Claude attached to terminal")
	managerMode := flag.Bool("manage", false, "Force-show the instance picker window even if 'show on startup' is off")
	flag.Parse()

	// Patcher mode has no config/UI dependency and must stay minimal - handled first.
	if *patcherMode {
		patcher.EmbeddedFS = EmbeddedFS
		patcher.Debug = *debugFlag
		os.Exit(runPatcherMode(*forceUpdateFlag, *debugFlag))
	}

	cfg, err := appconfig.Load()
	if err != nil {
		fmt.Printf("Warning: failed to load launcher-config.json, using defaults: %v\n", err)
	}

	var instanceName *string
	switch {
	case *instanceFlag != "":
		// Explicit --instance always wins and never shows the picker.
		instanceName = instanceFlag
		cfg.LastUsedInstance = *instanceName
		cfg.AddInstance(*instanceName)
		_ = cfg.Save()
	case *managerMode || cfg.ShowPickerOnStartup || cfg.LastUsedInstance == "":
		pick := ui.ShowPicker(cfg)
		if pick.Cancelled {
			fmt.Println("No instance selected, exiting.")
			os.Exit(0)
		}
		instanceName = &pick.InstanceName
	default:
		instanceName = &cfg.LastUsedInstance
	}

	forceUpdate := forceUpdateFlag
	if *forceUpdate == false {
		*forceUpdate = cfg.ForceUpdate
	}
	debug := debugFlag
	if *debug == false {
		*debug = cfg.DebugMode
	}

	launchClaudeInTerminal = *debug

	fmt.Printf("Claude_WebExtension_Launcher version: %s\n", Version)
	// Set version for selfupdate module
	selfupdate.CurrentVersion = Version

	// Set embedded FS and debug flag for patcher module
	patcher.EmbeddedFS = EmbeddedFS
	patcher.Debug = *debug

	// Handle update completion first
	selfupdate.FinishUpdateIfNeeded()

	// Platform-specific setup before the main flow
	if err := prepareAdminContext(); err != nil {
		fmt.Printf("Failed to prepare admin context: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Claude WebExtension Launcher starting...")
	fmt.Printf("Version: %s\n", Version)

	// Check for self-updates (can be turned off from Settings -> "Automatically
	// check for launcher updates on startup", or overridden per-run with --manage
	// if you just want the picker without forcing an update check).
	if cfg.AutoUpdate {
		if err := selfupdate.CheckAndUpdate(); err != nil {
			fmt.Printf("Update check failed: %v\n", err)
			// Continue anyway
		}
	} else {
		fmt.Println("Skipping self-update check (disabled in Settings)")
	}

	// Ensure Claude is patched and extensions are up-to-date.
	// On Windows this may invoke an elevated patcher subprocess via UAC.
	// On macOS this runs in-process.
	if cfg.SkipClaudeUpdateCheck {
		fmt.Println("Skipping Claude update/patch check (disabled in Settings)")
		if _, statErr := os.Stat(claudeExecutablePath()); statErr != nil {
			fmt.Println("Error: Claude is not installed yet and update checks are disabled. Turn 'Skip Claude update check' off in Settings for the first run.")
			os.Exit(1)
		}
	} else if err := ensureClaudeReady(*forceUpdate); err != nil {
		if _, statErr := os.Stat(claudeExecutablePath()); statErr == nil {
			fmt.Printf("Warning: %v\n", err)
			fmt.Println("Continuing with existing installation...")
		} else {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
	}

	// Release any platform-specific privileges before launching Claude
	releaseAdminContext()

	// Reconcile Cowork/Code session sharing before any uninstall prompt (Windows only):
	// repair a named instance that was wrongly pooled by an older build, then (only for the
	// default instance) share with the official install via junctions into a neutral store.
	RepairSessionSharing(*instanceName)
	SetupSessionSharing(*instanceName)

	// Check for official Claude MSIX installation (Windows only)
	checkMSIXAndPrompt(*instanceName)

	// Clear caches that interfere with extension loading and updates
	claudeDataDir := claudeUserDataDir(*instanceName)
	if claudeDataDir != "" {
		cacheDirs := []string{"Service Worker", "WebStorage", "Cache", "Code Cache"}
		fmt.Printf("Clearing cache folders:\n")
		for _, dir := range cacheDirs {
			p := filepath.Join(claudeDataDir, dir)
			fmt.Printf("  %s\n", p)
			os.RemoveAll(p)
		}
		fmt.Println("Cache cleared successfully")
	}

	// Launch Claude
	fmt.Println("Launching Claude.")
	claudePath := claudeExecutablePath()
	instanceArg := fmt.Sprintf("--instance=%s", *instanceName)

	if launchClaudeInTerminal {
		// In developer mode, run Claude in the same terminal to see debug output
		cmd := exec.Command(claudePath, instanceArg)
		cmd.Dir = filepath.Dir(claudePath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		cmd.Run()
	} else {
		// Launch detached
		cmd := exec.Command(claudePath, instanceArg)
		cmd.Dir = filepath.Dir(claudePath)
		cmd.Start()
	}
}
