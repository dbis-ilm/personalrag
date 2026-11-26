package main

import (
	"embed"
	"log"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/icons"

	"personalrag/internal/config"
)

// The built frontend (frontend/dist) is embedded into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// Typed events: the binding generator emits TS types for these.
	application.RegisterEvent[AnswerChunk](EventAnswerChunk)
	application.RegisterEvent[AnswerDone](EventAnswerDone)
	application.RegisterEvent[IndexProgress](EventIndexProgress)
	application.RegisterEvent[SourcesChanged](EventSourcesChanged)
	application.RegisterEvent[AppStatus](EventStatusChanged)
	application.RegisterEvent[Notice](EventNotice)
}

func main() {
	dataDir, err := config.DataDir()
	if err != nil {
		log.Fatalf("Datenverzeichnis: %v", err)
	}
	svc := NewAppService(dataDir)

	// One running app per data directory. A second launch hands over to the
	// first (which comes to the front) and exits, so two processes never
	// index into the same database.
	var window *application.WebviewWindow
	singleInstance := &application.SingleInstanceOptions{
		UniqueID: config.InstanceID(dataDir),
		OnSecondInstanceLaunch: func(application.SecondInstanceData) {
			if window != nil {
				window.Show()
				window.Focus()
			}
		},
	}

	app := application.New(application.Options{
		SingleInstance: singleInstance,
		Name:           "PersonalRAG",
		Description:    "Lokale Dokumentensuche mit belegten Antworten",
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			// Closing the window keeps the app running in the menu bar.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "main",
		Title:     "PersonalRAG",
		Width:     1280,
		Height:    820,
		MinWidth:  900,
		MinHeight: 600,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 44,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		URL:              "/",
		EnableFileDrop:   true,
	})

	// Folders dropped anywhere become collections; files inside a watched
	// folder are indexed first.
	window.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		if files := e.Context().DroppedFiles(); len(files) > 0 {
			go svc.AddPaths(files)
		}
	})

	// Hide instead of quit: indexing and the MCP server keep running.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})

	showWindow := func() {
		window.Show()
		window.Focus()
	}

	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(icons.SystrayMacTemplate)
	} else {
		tray.SetIcon(icons.SystrayLight)
	}
	tray.SetTooltip("PersonalRAG")

	menu := app.NewMenu()
	menu.Add("PersonalRAG öffnen").OnClick(func(*application.Context) { showWindow() })
	menu.AddSeparator()
	menu.AddCheckbox("Indexierung pausieren", false).OnClick(func(ctx *application.Context) {
		svc.SetIndexPaused(ctx.ClickedMenuItem().Checked())
	})
	menu.AddSeparator()
	menu.Add("Beenden").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
