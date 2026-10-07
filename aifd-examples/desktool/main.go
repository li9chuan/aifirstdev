package main

import (
	"embed"
	"io/fs"
	"log"

	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/desktop"
	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/shell"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	assetRoot, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.Fatal(err)
	}
	app := desktop.NewDesktopApp()
	if err := shell.Run(shell.Config{
		Title:     "DeskTool",
		Width:     1180,
		Height:    720,
		MinWidth:  960,
		MinHeight: 620,
		Assets:    assetRoot,
		Services:  []application.Service{application.NewService(app)},
	}); err != nil {
		log.Fatal(err)
	}
}
