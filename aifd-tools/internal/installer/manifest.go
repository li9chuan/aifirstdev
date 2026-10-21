package installer

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/li9chuan/aifirstdev/aifd-tools/internal/toolchain"
)

// 安装类型定义工具可被准备到本地的方式。
const (
	TypeArchive       = "archive"
	TypeGoInstall     = "go-install"
	TypeGitHubRelease = "github-release"
	TypeUnsupported   = "unsupported"
)

// Manifest 是从 JSON 读取的工具安装清单根结构。
type Manifest struct {
	Tools map[string]Tool `json:"tools"`
}

// Tool 描述一个外部工具的默认安装元数据。
type Tool struct {
	Version           string              `json:"version"`
	Type              string              `json:"type"`
	URL               string              `json:"url,omitempty"`
	Archive           string              `json:"archive,omitempty"`
	SHA256            string              `json:"sha256,omitempty"`
	Module            string              `json:"module,omitempty"`
	ReleaseAPI        string              `json:"release_api,omitempty"`
	AssetContains     []string            `json:"asset_contains,omitempty"`
	Bin               []string            `json:"bin,omitempty"`
	Include           []string            `json:"include,omitempty"`
	BinInclude        []string            `json:"bin_include,omitempty"`
	UnsupportedReason string              `json:"unsupported_reason,omitempty"`
	Platforms         map[string]Platform `json:"platforms,omitempty"`
}

// Platform 描述某个 GOOS-GOARCH 目标平台的安装元数据覆盖项。
type Platform struct {
	Version           string   `json:"version,omitempty"`
	Type              string   `json:"type,omitempty"`
	URL               string   `json:"url,omitempty"`
	Archive           string   `json:"archive,omitempty"`
	SHA256            string   `json:"sha256,omitempty"`
	Module            string   `json:"module,omitempty"`
	ReleaseAPI        string   `json:"release_api,omitempty"`
	AssetContains     []string `json:"asset_contains,omitempty"`
	Bin               []string `json:"bin,omitempty"`
	Include           []string `json:"include,omitempty"`
	BinInclude        []string `json:"bin_include,omitempty"`
	Platform          string   `json:"platform,omitempty"`
	UnsupportedReason string   `json:"unsupported_reason,omitempty"`
}

// Plan 是合并 lockfile 和平台信息后的最终安装指令。
type Plan struct {
	Name              string
	Version           string
	Type              string
	URL               string
	Archive           string
	SHA256            string
	Module            string
	ReleaseAPI        string
	AssetContains     []string
	Bin               []string
	Include           []string
	BinInclude        []string
	UnsupportedReason string
	GOOS              string
	GOARCH            string
}

// DefaultTools 返回本地 AIFD 工作流默认安装的工具集合。
func DefaultTools() []string {
	return []string{
		toolchain.ToolProtoc,
		toolchain.ToolProtocGenGo,
		toolchain.ToolLuban,
	}
}

// DefaultManifest 返回受支持工具的内置安装元数据。
func DefaultManifest() Manifest {
	return Manifest{Tools: map[string]Tool{
		toolchain.ToolProtoc: {
			Version: "36.2",
			Type:    TypeArchive,
			URL:     "https://github.com/protocolbuffers/protobuf/releases/download/v{version}/protoc-{version}-{platform}.zip",
			Archive: "zip",
			Bin:     exeCandidates("bin/protoc", "protoc"),
			Include: []string{"include"},
			Platforms: map[string]Platform{
				"windows-amd64": {Platform: "win64"},
				"windows-386":   {Platform: "win32"},
				"linux-amd64":   {Platform: "linux-x86_64"},
				"linux-arm64":   {Platform: "linux-aarch_64"},
				"darwin-amd64":  {Platform: "osx-x86_64"},
				"darwin-arm64":  {Platform: "osx-aarch_64"},
			},
		},
		toolchain.ToolProtocGenGo: {
			Version: "v1.36.12",
			Type:    TypeGoInstall,
			Module:  "google.golang.org/protobuf/cmd/protoc-gen-go",
			Bin:     exeCandidates("protoc-gen-go", "bin/protoc-gen-go"),
		},
		toolchain.ToolLuban: {
			Version:       "v4.8.0",
			Type:          TypeGitHubRelease,
			ReleaseAPI:    "https://api.github.com/repos/focus-creative-games/luban/releases/tags/{version}",
			Archive:       "zip",
			Bin:           lubanCandidates(),
			BinInclude:    []string{"Templates"},
			AssetContains: []string{"Luban", ".zip"},
			Platforms: map[string]Platform{
				"windows-amd64": {AssetContains: []string{"Luban", ".zip"}},
				"windows-386":   {AssetContains: []string{"Luban", ".zip"}},
				"linux-amd64":   {AssetContains: []string{"Luban", ".zip"}},
				"linux-arm64":   {AssetContains: []string{"Luban", ".zip"}},
				"darwin-amd64":  {AssetContains: []string{"Luban", ".zip"}},
				"darwin-arm64":  {AssetContains: []string{"Luban", ".zip"}},
			},
		},
		toolchain.ToolWails: {
			Version: "v3.0.0-beta.34",
			Type:    TypeGoInstall,
			Module:  "github.com/wailsapp/wails/v3/cmd/wails3",
			Bin:     exeCandidates("wails3", "bin/wails3"),
		},
		toolchain.ToolNSIS: {
			Version: "3.10",
			Type:    TypeArchive,
			URL:     "https://downloads.sourceforge.net/project/nsis/NSIS%203/{version}/nsis-{version}.zip",
			Archive: "zip",
			Bin:     exeCandidates("makensis", "bin/makensis", "Bin/makensis"),
			Platforms: map[string]Platform{
				"linux-amd64": {
					Type:              TypeUnsupported,
					UnsupportedReason: "NSIS makensis install is only managed by aifd-tools for Windows desktop packaging; use PATH, AIFD_MAKENSIS, or an OS package manager",
				},
				"linux-arm64": {
					Type:              TypeUnsupported,
					UnsupportedReason: "NSIS makensis install is only managed by aifd-tools for Windows desktop packaging; use PATH, AIFD_MAKENSIS, or an OS package manager",
				},
				"darwin-amd64": {
					Type:              TypeUnsupported,
					UnsupportedReason: "NSIS makensis install is only managed by aifd-tools for Windows desktop packaging; use PATH, AIFD_MAKENSIS, or an OS package manager",
				},
				"darwin-arm64": {
					Type:              TypeUnsupported,
					UnsupportedReason: "NSIS makensis install is only managed by aifd-tools for Windows desktop packaging; use PATH, AIFD_MAKENSIS, or an OS package manager",
				},
			},
		},
		toolchain.ToolRedis: {
			Version:           "8.6",
			Type:              TypeUnsupported,
			UnsupportedReason: "redis-server portable binary install is not managed by aifd-tools; use PATH, AIFD_REDIS_SERVER, Docker, WSL, or an OS package manager",
			Platforms: map[string]Platform{
				"windows-amd64": {UnsupportedReason: "redis-server is not installed by aifd-tools on Windows; use WSL, Memurai, Docker, PATH, or AIFD_REDIS_SERVER"},
				"windows-386":   {UnsupportedReason: "redis-server is not installed by aifd-tools on Windows; use WSL, Memurai, Docker, PATH, or AIFD_REDIS_SERVER"},
			},
		},
	}}
}

// LoadManifestFile 读取 JSON 安装清单，并规范化空工具表。
func LoadManifestFile(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	if manifest.Tools == nil {
		manifest.Tools = map[string]Tool{}
	}
	return manifest, nil
}

// Plan 为目标平台解析一个工具的安装计划。
func (m Manifest) Plan(name string, lockedVersion string, goos string, goarch string) (Plan, error) {
	def, ok := m.Tools[name]
	if !ok {
		return Plan{}, fmt.Errorf("tool %q is not in install manifest", name)
	}
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	platform := def.Platforms[goos+"-"+goarch]
	version := chooseVersion(lockedVersion, choose(platform.Version, def.Version))
	plan := Plan{
		Name:              name,
		Version:           version,
		Type:              choose(platform.Type, def.Type),
		URL:               choose(platform.URL, def.URL),
		Archive:           choose(platform.Archive, def.Archive),
		SHA256:            choose(platform.SHA256, def.SHA256),
		Module:            choose(platform.Module, def.Module),
		ReleaseAPI:        choose(platform.ReleaseAPI, def.ReleaseAPI),
		AssetContains:     chooseSlice(platform.AssetContains, def.AssetContains),
		Bin:               chooseSlice(platform.Bin, def.Bin),
		Include:           chooseSlice(platform.Include, def.Include),
		BinInclude:        chooseSlice(platform.BinInclude, def.BinInclude),
		UnsupportedReason: choose(platform.UnsupportedReason, def.UnsupportedReason),
		GOOS:              goos,
		GOARCH:            goarch,
	}
	replacer := strings.NewReplacer(
		"{version}", version,
		"{platform}", choose(platform.Platform, goos+"-"+goarch),
		"{goos}", goos,
		"{goarch}", goarch,
	)
	plan.URL = replacer.Replace(plan.URL)
	plan.ReleaseAPI = replacer.Replace(plan.ReleaseAPI)
	if plan.Type == "" {
		return Plan{}, fmt.Errorf("tool %q has empty install type", name)
	}
	if plan.Version == "" {
		return Plan{}, fmt.Errorf("tool %q has empty install version", name)
	}
	return plan, nil
}

// IsVersionPlaceholder 判断锁定版本是否应回退到 manifest 元数据。
func IsVersionPlaceholder(version string) bool {
	version = strings.TrimSpace(strings.ToLower(version))
	return version == "" || version == "any" || strings.Contains(version, "x") || strings.Contains(version, "*")
}

func chooseVersion(locked string, fallback string) string {
	if IsVersionPlaceholder(locked) {
		return strings.TrimSpace(fallback)
	}
	return strings.TrimSpace(locked)
}

func choose(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func chooseSlice(value []string, fallback []string) []string {
	if len(value) > 0 {
		return append([]string(nil), value...)
	}
	return append([]string(nil), fallback...)
}

func exeCandidates(names ...string) []string {
	if runtime.GOOS != "windows" {
		return names
	}
	out := make([]string, 0, len(names)*2)
	for _, name := range names {
		if strings.HasSuffix(strings.ToLower(name), ".exe") {
			out = append(out, name)
			continue
		}
		out = append(out, name+".exe", name)
	}
	return out
}

func lubanCandidates() []string {
	if runtime.GOOS == "windows" {
		return []string{
			"Luban.ClientServer.exe",
			"bin/Luban.ClientServer.exe",
			"luban.exe",
			"bin/luban.exe",
		}
	}
	return []string{
		"Luban.ClientServer",
		"bin/Luban.ClientServer",
		"luban",
		"bin/luban",
	}
}
