// Package ui implements the Chrome-profile-style instance picker and the
// settings page, both built with Fyne (pure Go, no CGO GUI toolkit needed
// beyond what Fyne itself requires).
package ui

import (
	"claude-webext-patcher/appconfig"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// PickResult is what the picker window returns once the user makes a choice.
type PickResult struct {
	InstanceName string
	Cancelled    bool
}

// ShowPicker renders the "Who's using Chrome?"-style window and blocks until
// the user picks an instance (or closes the window, which counts as cancel).
// cfg is mutated in place (new instances added, ShowPickerOnStartup toggled,
// LastUsedInstance updated) and saved to disk before returning.
func ShowPicker(cfg *appconfig.Config) PickResult {
	a := app.New()
	w := a.NewWindow("Claude WebExtension Launcher")
	w.Resize(fyne.NewSize(640, 420))

	result := PickResult{Cancelled: true}

	var cardsBox *fyne.Container
	var rebuildCards func()

	chooseInstance := func(name string) {
		result = PickResult{InstanceName: name}
		cfg.LastUsedInstance = name
		cfg.AddInstance(name)
		_ = cfg.Save()
		w.Close()
	}

	removeInstance := func(name string) {
		dialog.ShowConfirm("Remove instance",
			"Remove \""+name+"\" from the list?\n(This only forgets it here - it does not delete its Claude data folder.)",
			func(confirmed bool) {
				if !confirmed {
					return
				}
				cfg.RemoveInstance(name)
				_ = cfg.Save()
				rebuildCards()
			}, w)
	}

	makeInstanceCard := func(name string) fyne.CanvasObject {
		icon := widget.NewIcon(theme.AccountIcon())
		label := widget.NewLabelWithStyle(name, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

		menuBtn := widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), func() {
			removeInstance(name)
		})
		menuBtn.Importance = widget.LowImportance

		top := container.NewBorder(nil, nil, nil, menuBtn)
		content := container.NewVBox(
			top,
			container.NewCenter(icon),
			container.NewCenter(label),
		)

		bg := canvas.NewRectangle(theme.InputBackgroundColor())
		card := container.NewStack(bg, container.NewPadded(content))

		btn := widget.NewButton("", func() { chooseInstance(name) })
		btn.Importance = widget.LowImportance

		// btn must be BELOW card in the stack: it's a transparent full-card
		// hit target for "select this instance", but card (on top) still
		// contains its own tappable menuBtn. Fyne hit-tests top-down, so
		// menuBtn (topmost at its position) gets the click instead of being
		// swallowed by btn, and card's opaque background stops btn's hover
		// highlight from showing through and hiding the icon/label.
		return container.NewStack(btn, card)
	}

	makeAddCard := func() fyne.CanvasObject {
		plus := widget.NewLabelWithStyle("+", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
		addLabel := widget.NewLabel("Add")
		content := container.NewVBox(
			container.NewCenter(addLabel),
			container.NewCenter(plus),
		)

		btn := widget.NewButton("", func() {
			entry := widget.NewEntry()
			entry.SetPlaceHolder("Instance name (e.g. work, testing)")
			dialog.ShowForm("New instance", "Add", "Cancel",
				[]*widget.FormItem{widget.NewFormItem("Name", entry)},
				func(ok bool) {
					name := entry.Text
					if !ok || name == "" {
						return
					}
					cfg.AddInstance(name)
					_ = cfg.Save()
					rebuildCards()
				}, w)
		})
		btn.Importance = widget.LowImportance

		return container.NewStack(container.NewPadded(content), btn)
	}

	rebuildCards = func() {
		cardsBox.Objects = nil
		for _, name := range cfg.Instances {
			cardsBox.Add(makeInstanceCard(name))
		}
		cardsBox.Add(makeAddCard())
		cardsBox.Refresh()
	}

	cardsBox = container.New(layout.NewGridWrapLayout(fyne.NewSize(130, 130)))
	rebuildCards()

	title := widget.NewLabelWithStyle("Who's launching Claude?", fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true})
	subtitle := widget.NewLabelWithStyle("Pick an instance, or add a new one. Each instance keeps its own extensions and data.",
		fyne.TextAlignCenter, fyne.TextStyle{})
	subtitle.Wrapping = fyne.TextWrapWord
	subtitleBox := container.New(layout.NewGridWrapLayout(fyne.NewSize(560, 40)), subtitle)

	showOnStartup := widget.NewCheck("Show on startup", func(checked bool) {
		cfg.ShowPickerOnStartup = checked
		_ = cfg.Save()
	})
	showOnStartup.SetChecked(cfg.ShowPickerOnStartup)

	settingsBtn := widget.NewButtonWithIcon("Settings", theme.SettingsIcon(), func() {
		ShowSettings(a, cfg)
	})

	bottomBar := container.NewBorder(nil, nil, showOnStartup, settingsBtn)

	cardsScroll := container.NewVScroll(cardsBox)

	content := container.NewBorder(
		container.NewVBox(container.NewCenter(title), container.NewCenter(subtitleBox), widget.NewSeparator()),
		bottomBar,
		nil, nil,
		cardsScroll,
	)

	w.SetContent(container.NewPadded(content))
	w.CenterOnScreen()
	w.ShowAndRun()

	return result
}

// ShowSettings opens a small modal-ish settings window covering what used to
// be CLI-only flags.
func ShowSettings(a fyne.App, cfg *appconfig.Config) {
	w := a.NewWindow("Settings")
	w.Resize(fyne.NewSize(420, 260))

	autoUpdate := widget.NewCheck("Automatically check for launcher updates on startup", func(checked bool) {
		cfg.AutoUpdate = checked
	})
	autoUpdate.SetChecked(cfg.AutoUpdate)

	forceUpdate := widget.NewCheck("Force-update Claude even if not verified compatible", func(checked bool) {
		cfg.ForceUpdate = checked
	})
	forceUpdate.SetChecked(cfg.ForceUpdate)

	debugMode := widget.NewCheck("Debug mode (keep console open, attach Claude to terminal)", func(checked bool) {
		cfg.DebugMode = checked
	})
	debugMode.SetChecked(cfg.DebugMode)

	skipClaudeUpdateCheck := widget.NewCheck("Skip Claude update check (also skips the admin/UAC prompt)", func(checked bool) {
		cfg.SkipClaudeUpdateCheck = checked
	})
	skipClaudeUpdateCheck.SetChecked(cfg.SkipClaudeUpdateCheck)

	saveBtn := widget.NewButton("Save", func() {
		_ = cfg.Save()
		w.Close()
	})

	content := container.NewVBox(
		widget.NewLabelWithStyle("Launcher settings", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		autoUpdate,
		forceUpdate,
		debugMode,
		skipClaudeUpdateCheck,
		layout.NewSpacer(),
		saveBtn,
	)

	w.SetContent(container.NewPadded(content))
	w.Show()
}
