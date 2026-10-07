package toolchain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-tools/internal/lockfile"
)

// 工具名用于标识 AIFD 工作流依赖的外部可执行文件。
const (
	ToolGo          = "go"
	ToolProtoc      = "protoc"
	ToolProtocGenGo = "protoc-gen-go"
	ToolLuban       = "luban"
	ToolRedis       = "redis-server"
	ToolWails       = "wails"
	ToolNSIS        = "nsis"
)

// Spec 描述一个外部工具的定位和版本检查规则。
type Spec struct {
	Name          string
	Env           string
	ExeNames      []string
	VersionArgs   []string
	Optional      bool
	UnsupportedOS map[string]string
}

// Result 表示一个工具的解析路径、来源、版本和失败状态。
type Result struct {
	Name            string
	Path            string
	Source          string
	ExpectedVersion string
	DetectedVersion string
	Missing         bool
	Unsupported     bool
	Optional        bool
	Error           string
}

// Resolver 按 flag、环境变量、项目本地安装和 PATH 的顺序定位工具。
type Resolver struct {
	ProjectDir string
	Lock       lockfile.Lock
	Overrides  map[string]string
	LookPath   func(string) (string, error)
	Stat       func(string) (os.FileInfo, error)
	GOOS       string
	GOARCH     string
}

// DefaultSpecs 返回内置的工具解析规则。
func DefaultSpecs() []Spec {
	return []Spec{
		{Name: ToolGo, Env: "AIFD_GO", ExeNames: exeNames("go"), VersionArgs: []string{"version"}},
		{Name: ToolProtoc, Env: "AIFD_PROTOC", ExeNames: exeNames("protoc"), VersionArgs: []string{"--version"}},
		{Name: ToolProtocGenGo, Env: "AIFD_PROTOC_GEN_GO", ExeNames: exeNames("protoc-gen-go"), VersionArgs: []string{"--version"}},
		{Name: ToolLuban, Env: "AIFD_LUBAN", ExeNames: lubanNames(), VersionArgs: []string{"--version"}},
		{Name: ToolWails, Env: "AIFD_WAILS", ExeNames: exeNames("wails3"), VersionArgs: []string{"version"}, Optional: true},
		{Name: ToolNSIS, Env: "AIFD_MAKENSIS", ExeNames: exeNames("makensis"), VersionArgs: []string{"/VERSION"}, Optional: true},
		{
			Name:        ToolRedis,
			Env:         "AIFD_REDIS_SERVER",
			ExeNames:    exeNames("redis-server"),
			VersionArgs: []string{"--version"},
			UnsupportedOS: map[string]string{
				"windows": "redis-server is not installed by aifd-tools on Windows; use WSL, Memurai, Docker, PATH, or AIFD_REDIS_SERVER",
			},
		},
	}
}

// SpecByName 按稳定工具名返回默认工具规则。
func SpecByName(name string) (Spec, bool) {
	for _, spec := range DefaultSpecs() {
		if spec.Name == name {
			return spec, true
		}
	}
	return Spec{}, false
}

// ResolveAll 按解析顺序解析所有默认工具。
func (r Resolver) ResolveAll(ctx context.Context, checkVersion bool) []Result {
	specs := DefaultSpecs()
	results := make([]Result, 0, len(specs))
	for _, spec := range specs {
		results = append(results, r.Resolve(ctx, spec, checkVersion))
	}
	return results
}

// Resolve 定位一个工具，并按需执行版本检查命令。
func (r Resolver) Resolve(ctx context.Context, spec Spec, checkVersion bool) Result {
	result := Result{Name: spec.Name, ExpectedVersion: r.Lock.ToolVersion(spec.Name), Optional: spec.Optional}
	if spec.Name == ToolGo {
		result.ExpectedVersion = r.Lock.ToolVersion(ToolGo)
	}
	if r.LookPath == nil {
		r.LookPath = exec.LookPath
	}
	if r.Stat == nil {
		r.Stat = os.Stat
	}

	if path := strings.TrimSpace(r.Overrides[spec.Name]); path != "" {
		return r.resolvePath(ctx, spec, result, path, "flag", checkVersion)
	}
	if path := strings.TrimSpace(os.Getenv(spec.Env)); path != "" {
		return r.resolvePath(ctx, spec, result, path, "env:"+spec.Env, checkVersion)
	}
	if path := r.resolveProjectTool(spec, result.ExpectedVersion); path != "" {
		return r.resolvePath(ctx, spec, result, path, "project", checkVersion)
	}
	for _, exeName := range spec.ExeNames {
		path, err := r.LookPath(exeName)
		if err == nil && path != "" {
			return r.resolvePath(ctx, spec, result, path, "PATH", checkVersion)
		}
	}
	if reason := spec.UnsupportedOS[r.goos()]; reason != "" {
		result.Unsupported = true
		result.Error = reason
		return result
	}

	result.Missing = true
	result.Error = fmt.Sprintf("%s not found; set %s or install under %s", spec.Name, spec.Env, ProjectToolsDir(r.ProjectDir))
	return result
}

func (r Resolver) resolvePath(ctx context.Context, spec Spec, result Result, path string, source string, checkVersion bool) Result {
	if _, err := r.Stat(path); err != nil {
		result.Missing = true
		result.Error = fmt.Sprintf("%s path %q from %s is not usable: %v", spec.Name, path, source, err)
		return result
	}
	result.Path = path
	result.Source = source
	if checkVersion {
		version, err := DetectVersion(ctx, path, spec.VersionArgs)
		if err != nil {
			result.Error = err.Error()
		}
		result.DetectedVersion = version
		if !versionMatches(result.ExpectedVersion, version) {
			result.Error = fmt.Sprintf("version mismatch: expected %s, got %s", result.ExpectedVersion, version)
		}
	}
	return result
}

func versionMatches(expected string, detected string) bool {
	expected = strings.TrimSpace(expected)
	detected = strings.TrimSpace(detected)
	if expected == "" || detected == "" {
		return true
	}
	lower := strings.ToLower(expected)
	if lower == "any" || strings.Contains(lower, "x") || strings.Contains(expected, "*") {
		return true
	}
	if strings.Contains(detected, expected) {
		return true
	}
	trimmed := strings.TrimPrefix(expected, "v")
	return trimmed != expected && strings.Contains(detected, trimmed)
}

func (r Resolver) resolveProjectTool(spec Spec, version string) string {
	if r.ProjectDir == "" || version == "" {
		return ""
	}
	base := ProjectToolVersionDir(r.ProjectDir, r.goos(), r.goarch(), spec.Name, version)
	for _, exeName := range spec.ExeNames {
		path := filepath.Join(base, "bin", exeName)
		if _, err := r.Stat(path); err == nil {
			return path
		}
		path = filepath.Join(base, exeName)
		if _, err := r.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// DetectVersion 执行带超时的版本命令，并返回合并后的输出。
func DetectVersion(ctx context.Context, path string, args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	versionCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(versionCtx, path, args...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if versionCtx.Err() != nil {
		return text, versionCtx.Err()
	}
	if err != nil {
		if text == "" {
			return "", err
		}
		return text, nil
	}
	return text, nil
}

// ProjectToolsDir 返回项目本地工具安装目录。
func ProjectToolsDir(projectDir string) string {
	if projectDir == "" {
		projectDir = "."
	}
	return filepath.Join(projectDir, ".aifd", "tools")
}

// ProjectToolVersionDir 返回单个项目工具的版本化安装根目录。
func ProjectToolVersionDir(projectDir string, goos string, goarch string, name string, version string) string {
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return filepath.Join(ProjectToolsDir(projectDir), goos+"-"+goarch, name, version)
}

// ProjectToolBinDir 返回版本化工具安装目录下的 bin 目录。
func ProjectToolBinDir(projectDir string, goos string, goarch string, name string, version string) string {
	return filepath.Join(ProjectToolVersionDir(projectDir, goos, goarch, name, version), "bin")
}

// AnyMissing 将缺失或失败的必要工具汇总为一个错误。
func AnyMissing(results []Result) error {
	var missing []string
	for _, result := range results {
		if result.Unsupported {
			continue
		}
		if result.Optional {
			continue
		}
		if result.Missing || result.Error != "" {
			missing = append(missing, result.Name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return errors.New("tool check failed: " + strings.Join(missing, ", "))
}

func (r Resolver) goos() string {
	if r.GOOS != "" {
		return r.GOOS
	}
	return runtime.GOOS
}

func (r Resolver) goarch() string {
	if r.GOARCH != "" {
		return r.GOARCH
	}
	return runtime.GOARCH
}

func exeNames(name string) []string {
	if runtime.GOOS == "windows" {
		return []string{name + ".exe", name}
	}
	return []string{name}
}

func lubanNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"Luban.ClientServer.exe", "luban.exe", "Luban.ClientServer", "luban"}
	}
	return []string{"Luban.ClientServer", "luban"}
}
