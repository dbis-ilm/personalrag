package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/dbis-ilm/personalrag/internal/config"
	"github.com/dbis-ilm/personalrag/internal/i18n"
	"github.com/dbis-ilm/personalrag/internal/logging"
)

// version is set at release time with -ldflags "-X main.version=…".
var version = "0.1.0-dev"

// The built frontend (frontend/dist) is embedded into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

// Icons (sources in build/icon-src): the app icon for the About box, a
// monochrome template for the macOS menu bar, a colour icon for the
// Windows notification area.
var (
	//go:embed build/appicon.png
	appIcon []byte
	//go:embed build/tray-template.png
	trayTemplateIcon []byte
	//go:embed build/tray-windows.png
	trayWindowsIcon []byte
)

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
		log.Fatalf("data directory: %v", err)
	}

	if len(os.Args) > 1 && os.Args[1] == "--mcp" {
		os.Exit(runMCP(dataDir))
	}

	// Logging first, so Genkit and Wails write to the same file.
	// PERSONALRAG_LOG_LEVEL overrides the level from the settings.
	settings, _ := config.Load(dataDir)
	i18n.Set(i18n.Resolve(settings.Language)) // before the tray menu is built
	level := settings.LogLevel
	if env := os.Getenv("PERSONALRAG_LOG_LEVEL"); env != "" {
		level = env
	}
	logs, err := logging.Setup(dataDir, level)
	if err != nil {
		logs, err = logging.Setup(dataDir, "info")
	}
	if err != nil {
		log.Fatalf("log: %v", err)
	}
	defer logs.Close()
	slog.Info("starting", "version", version, "os", runtime.GOOS, "arch", runtime.GOARCH)

	svc := NewAppService(dataDir, logs)

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
		Description:    "Local document search with cited answers",
		Icon:           appIcon,
		Logger:         logs.Logger(slog.LevelWarn), // Wails logs each asset request at info
		// Panics in bound methods and Wails goroutines are logged with their
		// stack; fatal ones elsewhere reach logs/crash.log via the runtime.
		PanicHandler: func(d *application.PanicDetails) {
			slog.Error("panic", "err", d.Error, "stack", d.StackTrace)
		},
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
		tray.SetTemplateIcon(trayTemplateIcon)
	} else {
		tray.SetIcon(trayWindowsIcon)
	}
	tray.SetTooltip("PersonalRAG")

	menu := app.NewMenu()
	openItem := menu.Add(i18n.T("tray.open")).OnClick(func(*application.Context) { showWindow() })
	menu.AddSeparator()
	pauseItem := menu.AddCheckbox(i18n.T("tray.pause"), false).OnClick(func(ctx *application.Context) {
		svc.SetIndexPaused(ctx.ClickedMenuItem().Checked())
	})
	menu.AddSeparator()
	quitItem := menu.Add(i18n.T("tray.quit")).OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)
	svc.onLanguageChange(func() {
		openItem.SetLabel(i18n.T("tray.open"))
		pauseItem.SetLabel(i18n.T("tray.pause"))
		quitItem.SetLabel(i18n.T("tray.quit"))
		menu.Update()
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// runMCP serves the MCP tools over stdio for an agent client that started
// PersonalRAG with --mcp. It runs next to the app (or without it) on the
// same index, without a window, the single-instance lock or indexing.
// Stdout belongs to the protocol: logs go to logs/mcp.log and stderr.
func runMCP(dataDir string) int {
	settings, _ := config.Load(dataDir)
	level := settings.LogLevel
	if env := os.Getenv("PERSONALRAG_LOG_LEVEL"); env != "" {
		level = env
	}
	logs, err := logging.SetupWith(dataDir, level, logging.Options{FileName: "mcp.log", NoCrashFile: true})
	if err != nil {
		logs, _ = logging.SetupWith(dataDir, "info", logging.Options{FileName: "mcp.log", NoCrashFile: true})
	}
	if logs != nil {
		defer logs.Close()
	}
	slog.Info("mcp starting", "version", version)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	svc := NewAppService(dataDir, logs)
	svc.agentMode = true
	if err := svc.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
		fmt.Fprintln(os.Stderr, "PersonalRAG:", err)
		return 1
	}
	defer svc.ServiceShutdown()
	srv, err := svc.AgentServer()
	if err != nil {
		fmt.Fprintln(os.Stderr, "PersonalRAG is not ready:", err)
		return 1
	}
	if err := srv.MCP().Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		slog.Error("mcp stopped", "err", err)
		return 1
	}
	return 0
}
