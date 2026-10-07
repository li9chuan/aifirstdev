package shell

import (
	"testing"
	"testing/fstest"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type testService struct{}

func TestConfigValidateRejectsMissingRequiredFields(t *testing.T) {
	valid := Config{
		Title:    "DeskTool",
		Width:    1180,
		Height:   720,
		Assets:   fstest.MapFS{},
		Services: []application.Service{application.NewService(&testService{})},
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "title", mutate: func(c *Config) { c.Title = "" }},
		{name: "size", mutate: func(c *Config) { c.Width = 0 }},
		{name: "assets", mutate: func(c *Config) { c.Assets = nil }},
		{name: "services", mutate: func(c *Config) { c.Services = nil }},
		{name: "zero service", mutate: func(c *Config) { c.Services = []application.Service{{}} }},
		{name: "nil service pointer", mutate: func(c *Config) { c.Services = []application.Service{application.NewService((*testService)(nil))} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := valid
			tt.mutate(&config)
			if err := config.Validate(); err == nil {
				t.Fatal("Validate error = nil, want error")
			}
		})
	}
}

func TestConfigValidateAcceptsMinimalConfig(t *testing.T) {
	config := Config{
		Title:    "DeskTool",
		Width:    1180,
		Height:   720,
		Assets:   fstest.MapFS{},
		Services: []application.Service{application.NewService(&testService{})},
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate error = %v", err)
	}
}
