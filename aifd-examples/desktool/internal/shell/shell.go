package shell

import (
	"context"
	"fmt"
	"io/fs"
	"reflect"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Lifecycle 收拢 Wails 桌面壳生命周期回调。
type Lifecycle struct {
	OnStartup     func(ctx context.Context)
	OnDomReady    func(ctx context.Context)
	OnShutdown    func(ctx context.Context)
	OnBeforeClose func(ctx context.Context) bool
}

// Config 是 desktool 内部 Wails 壳配置。
type Config struct {
	Title     string
	Width     int
	Height    int
	MinWidth  int
	MinHeight int
	Assets    fs.FS
	Services  []application.Service
	Lifecycle Lifecycle
}

// Validate 检查壳配置是否足够启动 Wails。
func (c Config) Validate() error {
	if c.Title == "" {
		return fmt.Errorf("shell title is required")
	}
	if c.Width <= 0 || c.Height <= 0 {
		return fmt.Errorf("shell size must be positive")
	}
	if c.MinWidth < 0 || c.MinHeight < 0 {
		return fmt.Errorf("shell min size must not be negative")
	}
	if c.Assets == nil {
		return fmt.Errorf("shell assets are required")
	}
	if len(c.Services) == 0 {
		return fmt.Errorf("shell services are required")
	}
	for i, service := range c.Services {
		instance := reflect.ValueOf(service.Instance())
		if !instance.IsValid() || instance.Kind() != reflect.Pointer || instance.IsNil() {
			return fmt.Errorf("shell service %d requires a non-nil pointer", i)
		}
	}
	return nil
}

// Run 创建 Wails 3 应用和主窗口，并连接桌面壳生命周期。
func Run(config Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	app := application.New(application.Options{
		Name:     config.Title,
		Services: config.Services,
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(config.Assets),
		},
		Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "main", Title: config.Title,
		Width: config.Width, Height: config.Height,
		MinWidth: config.MinWidth, MinHeight: config.MinHeight,
	})
	if config.Lifecycle.OnStartup != nil {
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
			config.Lifecycle.OnStartup(app.Context())
		})
	}
	var ready sync.Once
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		ready.Do(func() {
			if config.Lifecycle.OnDomReady != nil {
				config.Lifecycle.OnDomReady(app.Context())
			}
		})
	})
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if app.Context().Err() == nil && config.Lifecycle.OnBeforeClose != nil && config.Lifecycle.OnBeforeClose(app.Context()) {
			event.Cancel()
		}
	})
	if config.Lifecycle.OnShutdown != nil {
		app.OnShutdown(func() { config.Lifecycle.OnShutdown(app.Context()) })
	}
	return app.Run()
}
