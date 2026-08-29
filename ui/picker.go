// Package ui implements the Chrome-profile-style instance picker and the
// settings page, both built with Fyne (pure Go, no CGO GUI toolkit needed
// beyond what Fyne itself requires).
package ui

import (
	"image/color"
	"time"

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

// Design tokens. Pulled from the "Minimalism & Swiss Style" system (best fit
// for an internal tool/dashboard): navy primary, calm neutral background,
// green/amber/red reserved strictly for usage-level meaning, never decoration.
var (
	colorBorder      = color.NRGBA{R: 0xE4, G: 0xE7, B: 0xEB, A: 0xFF}
	colorMuted       = color.NRGBA{R: 0x47, G: 0x55, B: 0x69, A: 0xFF}
	colorUsageOK     = color.NRGBA{R: 0x05, G: 0x96, B: 0x69, A: 0xFF} // < 60%
	colorUsageWarn   = color.NRGBA{R: 0xD9, G: 0x8C, B: 0x0D, A: 0xFF} // 60-89%
	colorUsageHigh   = color.NRGBA{R: 0xDC, G: 0x26, B: 0x26, A: 0xFF} // >= 90%

	// Slightly larger than the card's actual content needs, to leave room
	// for the container.NewPadded wrapper added around each card in
	// rebuildCards (that padding is what creates the visible gap between
	// cards in the GridWrapLayout, at the cost of a bit of cell size).
	cardMinSize  = fyne.NewSize(256, 236)
	cardBottomGap = float32(20) // margin below the last row of cards
	usageRefresh = time.Minute
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
	w.Resize(fyne.NewSize(900, 560))

	result := PickResult{Cancelled: true}
	notes := loadNotes()

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
			"Remove \""+name+"\" from the list?\n(This only forgets it here — it does not delete its Claude data folder or its notes.)",
			func(confirmed bool) {
				if !confirmed {
					return
				}
				cfg.RemoveInstance(name)
				_ = cfg.Save()
				rebuildCards()
			}, w)
	}

	// usageBar renders a labeled percentage row: "5h  [=====     ] 41%".
	usageBar := func(label string, pct int, has bool) fyne.CanvasObject {
		nameLbl := widget.NewLabel(label)
		nameLbl.Alignment = fyne.TextAlignLeading

		if !has {
			dash := widget.NewLabel("—")
			dash.Alignment = fyne.TextAlignTrailing
			return container.NewBorder(nil, nil, nameLbl, nil, dash)
		}

		bar := widget.NewProgressBar()
		bar.Min, bar.Max = 0, 100
		bar.Value = float64(pct)

		pctColor := colorUsageOK
		switch {
		case pct >= 90:
			pctColor = colorUsageHigh
		case pct >= 60:
			pctColor = colorUsageWarn
		}
		pctLbl := canvas.NewText(itoa(pct)+"%", pctColor)
		pctLbl.TextStyle = fyne.TextStyle{Bold: true}

		row := container.NewBorder(nil, nil, nameLbl, pctLbl, bar)
		return row
	}

	// buildUsageSection reads fresh data from disk each call — cheap (one
	// small JSON file) and called at most once/minute per visible card, so
	// no caching layer is needed.
	buildUsageSection := func(name string) fyne.CanvasObject {
		u := ReadInstanceUsage(name)

		lastActive := widget.NewLabel("Last active: " + RelativeTime(u.LastActive))
		lastActive.TextStyle = fyne.TextStyle{Italic: true}

		return container.NewVBox(
			usageBar("5h", u.FiveHourPct, u.HasUsage),
			usageBar("7d", u.WeeklyPct, u.HasUsage),
			lastActive,
		)
	}

	makeInstanceCard := func(name string) fyne.CanvasObject {
		icon := widget.NewIcon(theme.AccountIcon())
		label := widget.NewLabelWithStyle(name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

		menuBtn := widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), func() {
			removeInstance(name)
		})
		menuBtn.Importance = widget.LowImportance

		header := container.NewBorder(nil, nil,
			container.NewHBox(icon, label), menuBtn)

		usageSection := buildUsageSection(name)

		entry := notes.get(name)
		quickNote := makeQuickNoteEntry(name, entry.QuickNote)

		notesBtn := widget.NewButtonWithIcon("Notes", theme.DocumentIcon(), func() {
			ShowFullNoteEditor(a, name)
		})
		notesBtn.Importance = widget.LowImportance

		launchBtn := widget.NewButton("Launch", func() { chooseInstance(name) })
		launchBtn.Importance = widget.HighImportance

		footer := container.NewBorder(nil, nil, notesBtn, launchBtn)

		body := container.NewVBox(
			header,
			widget.NewSeparator(),
			usageSection,
			quickNote,
			footer,
		)

		bg := canvas.NewRectangle(theme.InputBackgroundColor())
		bg.StrokeColor = colorBorder
		bg.StrokeWidth = 1
		bg.CornerRadius = 8

		padded := container.NewPadded(body)
		// cardsBox (GridWrapLayout, cardMinSize) is what enforces sizing —
		// this card just needs to be a single stacked CanvasObject.
		return container.NewStack(bg, padded)
	}

	makeAddCard := func() fyne.CanvasObject {
		plus := widget.NewLabelWithStyle("+", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
		addLabel := widget.NewLabel("Add instance")
		content := container.NewVBox(
			layout.NewSpacer(),
			container.NewCenter(plus),
			container.NewCenter(addLabel),
			layout.NewSpacer(),
		)

		bg := canvas.NewRectangle(color.Transparent)
		bg.StrokeColor = colorBorder
		bg.StrokeWidth = 1
		bg.CornerRadius = 8

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

		return container.NewStack(bg, container.NewPadded(content), btn)
	}

	rebuildCards = func() {
		notes = loadNotes() // pick up anything saved from a just-closed note editor
		cardsBox.Objects = nil
		// Each card is wrapped in container.NewPadded here (not inside
		// makeInstanceCard/makeAddCard themselves) so the extra space shows
		// up as a gap BETWEEN cards in the GridWrapLayout, rather than as
		// inner padding that would just make each card bigger.
		for _, name := range sortedInstanceNames(cfg.Instances) {
			cardsBox.Add(container.NewPadded(makeInstanceCard(name)))
		}
		cardsBox.Add(container.NewPadded(makeAddCard()))
		cardsBox.Refresh()
	}

	cardsBox = container.New(layout.NewGridWrapLayout(cardMinSize))
	rebuildCards()

	title := widget.NewLabelWithStyle("Who's launching Claude?", fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true})
	subtitle := widget.NewLabelWithStyle(
		"Pick an instance, or add a new one. Each instance keeps its own extensions, data and notes.",
		fyne.TextAlignCenter, fyne.TextStyle{})
	subtitle.Wrapping = fyne.TextWrapWord
	// Fyne quirk: a wrapping Label placed inside container.NewCenter (or any
	// layout that sizes to MinSize) collapses to ~1 character of width and
	// renders vertically. Force a fixed, generous width so it wraps normally.
	subtitleBox := container.New(layout.NewGridWrapLayout(fyne.NewSize(700, 40)), subtitle)

	refreshBtn := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), rebuildCards)
	refreshBtn.Importance = widget.LowImportance

	headerRow := container.NewBorder(nil, nil, nil, refreshBtn,
		container.NewVBox(container.NewCenter(title), container.NewCenter(subtitleBox)))

	showOnStartup := widget.NewCheck("Show on startup", func(checked bool) {
		cfg.ShowPickerOnStartup = checked
		_ = cfg.Save()
	})
	showOnStartup.SetChecked(cfg.ShowPickerOnStartup)

	settingsBtn := widget.NewButtonWithIcon("Settings", theme.SettingsIcon(), func() {
		ShowSettings(a, cfg)
	})

	bottomBar := container.NewBorder(nil, nil, showOnStartup, settingsBtn)

	// bottomSpacer gives the scroll area breathing room below the last row
	// of cards, so it doesn't sit flush against bottomBar (the "Show on
	// startup" / "Settings" row) when scrolled all the way down.
	bottomSpacer := canvas.NewRectangle(color.Transparent)
	bottomSpacer.SetMinSize(fyne.NewSize(0, cardBottomGap))
	cardsScroll := container.NewVScroll(container.NewVBox(cardsBox, bottomSpacer))

	content := container.NewBorder(
		container.NewVBox(headerRow, widget.NewSeparator()),
		bottomBar,
		nil, nil,
		cardsScroll,
	)

	w.SetContent(container.NewPadded(content))
	w.CenterOnScreen()

	// Auto-refresh usage numbers + last-active labels once a minute while
	// the picker is open, so the window doesn't need to be reopened to see
	// updated percentages. Manual refresh is also available via refreshBtn.
	stopRefresh := make(chan struct{})
	go func() {
		ticker := time.NewTicker(usageRefresh)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				rebuildCards()
			case <-stopRefresh:
				return
			}
		}
	}()
	defer close(stopRefresh)

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
