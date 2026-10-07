package installer

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-tools/internal/lockfile"
	"github.com/li9chuan/aifirstdev/aifd-tools/internal/toolchain"
)

// 安装状态表示单个请求工具的处理结果。
const (
	StatusInstalled   = "installed"
	StatusSkipped     = "skipped"
	StatusDryRun      = "dry-run"
	StatusUnsupported = "unsupported"
	StatusFailed      = "failed"
)

// ErrUnsupported 表示安装器有意不托管的工具。
var ErrUnsupported = errors.New("unsupported tool")

// Result 是返回给 CLI 的单工具安装结果。
type Result struct {
	Tool    string
	Version string
	Status  string
	Target  string
	URL     string
	Message string
	Err     error
}

// Options 配置工具安装来源、目标平台和测试钩子。
type Options struct {
	ProjectDir string
	Lock       lockfile.Lock
	Manifest   Manifest
	Tools      []string
	Force      bool
	DryRun     bool
	Timeout    time.Duration
	GOOS       string
	GOARCH     string
	GoExe      string

	Download  func(context.Context, string) ([]byte, error)
	GoInstall func(context.Context, string, string, string) error
}

type installRecord struct {
	Tool        string `json:"tool"`
	Version     string `json:"version"`
	Type        string `json:"type"`
	SourceURL   string `json:"source_url,omitempty"`
	GOOS        string `json:"goos"`
	GOARCH      string `json:"goarch"`
	InstalledAt string `json:"installed_at"`
}

// Install 执行已解析的安装计划，并返回更新后的 lock 快照。
func Install(ctx context.Context, opts Options) (lockfile.Lock, []Result) {
	if opts.ProjectDir == "" {
		opts.ProjectDir = "."
	}
	if opts.Manifest.Tools == nil {
		opts.Manifest = DefaultManifest()
	}
	if len(opts.Tools) == 0 {
		opts.Tools = DefaultTools()
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtime.GOARCH
	}
	if opts.Download == nil {
		client := &http.Client{Timeout: opts.Timeout}
		opts.Download = func(ctx context.Context, url string) ([]byte, error) {
			return download(ctx, client, url)
		}
	}
	if opts.GoInstall == nil {
		opts.GoInstall = runGoInstall
	}

	nextLock := opts.Lock
	results := make([]Result, 0, len(opts.Tools))
	for _, name := range opts.Tools {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		lockedVersion := opts.Lock.ToolVersion(name)
		plan, err := opts.Manifest.Plan(name, lockedVersion, opts.GOOS, opts.GOARCH)
		if err != nil {
			results = append(results, Result{Tool: name, Status: StatusFailed, Err: err, Message: err.Error()})
			continue
		}
		result := installOne(ctx, opts, plan)
		results = append(results, result)
		if result.Err == nil && !opts.DryRun && result.Status != StatusUnsupported {
			nextLock.SetToolVersion(name, plan.Version)
		}
	}
	return nextLock, results
}

func installOne(ctx context.Context, opts Options, plan Plan) Result {
	target := toolchain.ProjectToolVersionDir(opts.ProjectDir, opts.GOOS, opts.GOARCH, plan.Name, plan.Version)
	result := Result{
		Tool:    plan.Name,
		Version: plan.Version,
		Target:  target,
		URL:     plan.URL,
	}
	if plan.Type == TypeUnsupported {
		result.Status = StatusUnsupported
		result.Err = ErrUnsupported
		result.Message = plan.UnsupportedReason
		return result
	}
	if opts.DryRun {
		result.Status = StatusDryRun
		result.Message = "would install to " + target
		return result
	}
	if exists(target) && !opts.Force {
		result.Status = StatusSkipped
		result.Message = "already installed"
		return result
	}

	switch plan.Type {
	case TypeArchive:
		return installArchive(ctx, opts, plan, target)
	case TypeGitHubRelease:
		if plan.URL == "" {
			url, err := resolveGitHubReleaseAsset(ctx, opts.Download, plan)
			if err != nil {
				result.Status = StatusFailed
				result.Err = err
				result.Message = err.Error()
				return result
			}
			plan.URL = url
			result.URL = url
		}
		return installArchive(ctx, opts, plan, target)
	case TypeGoInstall:
		return installGoTool(ctx, opts, plan, target)
	default:
		err := fmt.Errorf("unknown install type %q for %s", plan.Type, plan.Name)
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
}

func installArchive(ctx context.Context, opts Options, plan Plan, target string) Result {
	result := Result{Tool: plan.Name, Version: plan.Version, Target: target, URL: plan.URL}
	if plan.URL == "" {
		err := fmt.Errorf("%s has empty download URL", plan.Name)
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
	data, err := opts.Download(ctx, plan.URL)
	if err != nil {
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
	if plan.SHA256 != "" {
		if err := verifySHA256(data, plan.SHA256); err != nil {
			result.Status = StatusFailed
			result.Err = err
			result.Message = err.Error()
			return result
		}
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0755); err != nil {
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
	workDir, err := os.MkdirTemp(parent, "."+plan.Name+"-*")
	if err != nil {
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
	defer os.RemoveAll(workDir)

	extractDir := filepath.Join(workDir, "extract")
	finalDir := filepath.Join(workDir, "final")
	if err := extractArchive(data, plan.Archive, extractDir); err != nil {
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
	if err := buildFinalDir(plan, extractDir, finalDir); err != nil {
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
	if err := writeInstallRecord(plan, finalDir); err != nil {
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
	if err := replaceDir(finalDir, target, opts.Force); err != nil {
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
	result.Status = StatusInstalled
	result.Message = "installed"
	return result
}

func installGoTool(ctx context.Context, opts Options, plan Plan, target string) Result {
	result := Result{Tool: plan.Name, Version: plan.Version, Target: target}
	if plan.Module == "" {
		err := fmt.Errorf("%s has empty go install module", plan.Name)
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0755); err != nil {
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
	workDir, err := os.MkdirTemp(parent, "."+plan.Name+"-*")
	if err != nil {
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
	defer os.RemoveAll(workDir)

	finalDir := filepath.Join(workDir, "final")
	binDir := filepath.Join(finalDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
	goExe := opts.GoExe
	if goExe == "" {
		goExe = "go"
	}
	if err := opts.GoInstall(ctx, goExe, plan.Module+"@"+plan.Version, binDir); err != nil {
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
	if err := writeInstallRecord(plan, finalDir); err != nil {
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
	if err := replaceDir(finalDir, target, opts.Force); err != nil {
		result.Status = StatusFailed
		result.Err = err
		result.Message = err.Error()
		return result
	}
	result.Status = StatusInstalled
	result.Message = "installed"
	return result
}

func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download %s failed: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func runGoInstall(ctx context.Context, goExe string, moduleVersion string, gobin string) error {
	cmd := exec.CommandContext(ctx, goExe, "install", moduleVersion)
	cmd.Env = append(os.Environ(), "GOBIN="+gobin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, text)
	}
	return nil
}

func verifySHA256(data []byte, expected string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	expected = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(expected)), "sha256:")
	if got != expected {
		return fmt.Errorf("sha256 mismatch: expected %s, got %s", expected, got)
	}
	return nil
}

func extractArchive(data []byte, kind string, dest string) error {
	switch strings.ToLower(kind) {
	case "zip", "":
		return extractZip(data, dest)
	case "tar.gz", "tgz":
		return extractTarGz(data, dest)
	default:
		return fmt.Errorf("unsupported archive type %q", kind)
	}
}

func extractZip(data []byte, dest string) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, file := range reader.File {
		target, err := safeArchivePath(dest, file.Name)
		if err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		in, err := file.Open()
		if err != nil {
			return err
		}
		err = writeFileFromReader(target, in, file.FileInfo().Mode())
		closeErr := in.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func extractTarGz(data []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeArchivePath(dest, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			if err := writeFileFromReader(target, tr, os.FileMode(header.Mode)); err != nil {
				return err
			}
		default:
			continue
		}
	}
}

func safeArchivePath(dest string, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	target := filepath.Join(dest, clean)
	rel, err := filepath.Rel(dest, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return target, nil
}

func writeFileFromReader(path string, src io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, src)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func buildFinalDir(plan Plan, extractDir string, finalDir string) error {
	binDir := filepath.Join(finalDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return err
	}
	if len(plan.Bin) > 0 {
		src, err := findAnyCandidate(extractDir, plan.Bin)
		if err != nil {
			return err
		}
		if err := copyDirFiles(filepath.Dir(src), binDir); err != nil {
			return err
		}
	}
	for _, include := range plan.Include {
		src, err := findDirCandidate(extractDir, include)
		if err != nil {
			return err
		}
		dst := filepath.Join(finalDir, filepath.Base(src))
		if filepath.Base(src) != "include" {
			dst = filepath.Join(finalDir, "include", filepath.Base(src))
		}
		if err := copyDir(src, dst); err != nil {
			return err
		}
	}
	for _, include := range plan.BinInclude {
		src, err := findDirCandidate(extractDir, include)
		if err != nil {
			return err
		}
		dst := filepath.Join(binDir, filepath.Base(src))
		if err := copyDir(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func findAnyCandidate(root string, candidates []string) (string, error) {
	var errs []string
	for _, candidate := range candidates {
		src, err := findCandidate(root, candidate)
		if err == nil {
			return src, nil
		}
		errs = append(errs, err.Error())
	}
	return "", fmt.Errorf("none of binary candidates found: %s", strings.Join(errs, "; "))
}

func findCandidate(root string, candidate string) (string, error) {
	direct := filepath.Join(root, filepath.FromSlash(candidate))
	if info, err := os.Stat(direct); err == nil && !info.IsDir() {
		return direct, nil
	}
	base := filepath.Base(filepath.FromSlash(candidate))
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Base(path), base) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("binary %q not found in archive", candidate)
	}
	return found, nil
}

func findDirCandidate(root string, candidate string) (string, error) {
	direct := filepath.Join(root, filepath.FromSlash(candidate))
	if info, err := os.Stat(direct); err == nil && info.IsDir() {
		return direct, nil
	}
	base := filepath.Base(filepath.FromSlash(candidate))
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Base(path), base) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("directory %q not found in archive", candidate)
	}
	return found, nil
}

func copyFile(src string, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func copyDir(src string, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyDirFiles(src string, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := copyFile(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name()), info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func replaceDir(src string, dst string, force bool) error {
	if exists(dst) {
		if !force {
			return fmt.Errorf("%s already exists", dst)
		}
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
	}
	return os.Rename(src, dst)
}

func writeInstallRecord(plan Plan, dir string) error {
	record := installRecord{
		Tool:        plan.Name,
		Version:     plan.Version,
		Type:        plan.Type,
		SourceURL:   plan.URL,
		GOOS:        plan.GOOS,
		GOARCH:      plan.GOARCH,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, "install.json"), data, 0644)
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func resolveGitHubReleaseAsset(ctx context.Context, downloader func(context.Context, string) ([]byte, error), plan Plan) (string, error) {
	release, err := downloadGitHubRelease(ctx, downloader, plan)
	if err != nil {
		return "", err
	}
	return findGitHubReleaseAssetURL(plan, release)
}

func downloadGitHubRelease(ctx context.Context, downloader func(context.Context, string) ([]byte, error), plan Plan) (githubRelease, error) {
	if plan.ReleaseAPI == "" {
		return githubRelease{}, fmt.Errorf("%s has empty release_api", plan.Name)
	}
	data, err := downloader(ctx, plan.ReleaseAPI)
	if err != nil {
		return githubRelease{}, err
	}
	var release githubRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return githubRelease{}, err
	}
	return release, nil
}

func findGitHubReleaseAssetURL(plan Plan, release githubRelease) (string, error) {
	var names []string
	for _, asset := range release.Assets {
		names = append(names, asset.Name)
		if assetMatches(asset.Name, plan.AssetContains) {
			if asset.BrowserDownloadURL == "" {
				return "", fmt.Errorf("release asset %s has empty browser_download_url", asset.Name)
			}
			return asset.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("no release asset matched %s; assets=%s", strings.Join(plan.AssetContains, ","), strings.Join(names, ","))
}

func assetMatches(name string, contains []string) bool {
	name = strings.ToLower(name)
	for _, part := range contains {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		if !strings.Contains(name, part) {
			return false
		}
	}
	return true
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
