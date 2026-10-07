package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const defaultTimeout = 30 * time.Second

// UpdateInfo 是前端可直接展示的更新检查结果。
type UpdateInfo struct {
	Configured     bool     `json:"configured"`
	Available      bool     `json:"available"`
	Unsupported    bool     `json:"unsupported"`
	Invalid        bool     `json:"invalid"`
	CurrentVersion string   `json:"currentVersion"`
	Version        string   `json:"version"`
	MinVersion     string   `json:"minVersion"`
	Force          bool     `json:"force"`
	URL            string   `json:"url"`
	SHA256         string   `json:"sha256"`
	Notes          []string `json:"notes"`
	Message        string   `json:"message"`
}

// DownloadResult 是更新包下载结果。
type DownloadResult struct {
	OK      bool       `json:"ok"`
	File    string     `json:"file"`
	Bytes   int64      `json:"bytes"`
	Update  UpdateInfo `json:"update"`
	Message string     `json:"message"`
}

// UpdateResult 是安装动作结果。
type UpdateResult struct {
	OK          bool   `json:"ok"`
	Unsupported bool   `json:"unsupported"`
	Message     string `json:"message"`
}

type manifest struct {
	Version    string   `json:"version"`
	MinVersion string   `json:"minVersion"`
	Force      bool     `json:"force"`
	URL        string   `json:"url"`
	SHA256     string   `json:"sha256"`
	Notes      []string `json:"notes"`
}

type validationOptions struct {
	requireURL bool
}

var httpClient = &http.Client{Timeout: defaultTimeout}
var currentGOOS = runtime.GOOS
var startInstaller = startProcess

// Check 读取远端 manifest 并判断是否存在新版本。
func Check(manifestURL, currentVersion string) UpdateInfo {
	info := UpdateInfo{CurrentVersion: currentVersion}
	if strings.TrimSpace(manifestURL) == "" {
		info.Message = "未配置更新源"
		return info
	}
	info.Configured = true
	resp, err := httpClient.Get(manifestURL)
	if err != nil {
		info.Message = "检查更新失败：" + err.Error()
		return info
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		info.Message = fmt.Sprintf("检查更新失败：HTTP %d", resp.StatusCode)
		return info
	}
	var m manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		info.Message = "更新清单解析失败：" + err.Error()
		return info
	}
	m.normalize()
	info.Version = m.Version
	info.MinVersion = m.MinVersion
	info.URL = m.URL
	info.SHA256 = m.SHA256
	info.Notes = append([]string(nil), m.Notes...)
	if err := validateManifest(m, validationOptions{}); err != nil {
		info.Invalid = true
		info.Message = "更新清单无效：" + err.Error()
		return info
	}
	info.Available = compareVersion(m.Version, currentVersion) > 0
	info.Force = m.Force || (m.MinVersion != "" && compareVersion(currentVersion, m.MinVersion) < 0)
	if !info.Available {
		info.Message = "当前已是最新版本"
		return info
	}
	if err := validateManifest(m, validationOptions{requireURL: true}); err != nil {
		info.Invalid = true
		info.Available = false
		info.Message = "更新清单无效：" + err.Error()
		return info
	}
	info.Message = "发现新版本"
	return info
}

// Download 下载更新安装包，manifest 带 sha256 时会自动校验。
func Download(update UpdateInfo) DownloadResult {
	result := DownloadResult{Update: update}
	if !update.Available {
		result.Message = "没有可下载的更新"
		return result
	}
	if strings.TrimSpace(update.URL) == "" {
		result.Message = "更新包地址为空"
		return result
	}
	if err := validateInstallerURL(update.URL); err != nil {
		result.Message = "更新包地址无效：" + err.Error()
		return result
	}
	resp, err := httpClient.Get(update.URL)
	if err != nil {
		result.Message = "下载更新失败：" + err.Error()
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Message = fmt.Sprintf("下载更新失败：HTTP %d", resp.StatusCode)
		return result
	}
	file, err := os.CreateTemp("", "desktool-update-*"+downloadExt(update.URL))
	if err != nil {
		result.Message = "创建临时文件失败：" + err.Error()
		return result
	}
	path := file.Name()
	bytes, copyErr := io.Copy(file, resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		result.Message = "写入更新包失败：" + copyErr.Error()
		return result
	}
	if closeErr != nil {
		_ = os.Remove(path)
		result.Message = "关闭更新包失败：" + closeErr.Error()
		return result
	}
	if update.SHA256 != "" {
		if err := Verify(path, update.SHA256); err != nil {
			_ = os.Remove(path)
			result.Message = "更新包校验失败：" + err.Error()
			return result
		}
	}
	result.OK = true
	result.File = path
	result.Bytes = bytes
	result.Message = "更新包已下载"
	return result
}

// Verify 校验文件 sha256。
func Verify(file, wantSHA256 string) error {
	want := normalizeSHA256(wantSHA256)
	if err := validateSHA256(want); err != nil {
		return err
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return err
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != want {
		return fmt.Errorf("sha256 mismatch: got %s", got)
	}
	return nil
}

// Install 启动平台安装器。首版只有 Windows NSIS installer 是支持路径。
func Install(file string) error {
	if currentGOOS != "windows" {
		return fmt.Errorf("unsupported platform: %s", currentGOOS)
	}
	if strings.TrimSpace(file) == "" {
		return fmt.Errorf("installer file is empty")
	}
	if filepath.Ext(file) != ".exe" {
		return fmt.Errorf("windows installer must be .exe")
	}
	if _, err := os.Stat(file); err != nil {
		return err
	}
	return startInstaller(file)
}

func downloadExt(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ".bin"
	}
	ext := filepath.Ext(parsed.Path)
	if ext == "" {
		return ".bin"
	}
	return ext
}

func (m *manifest) normalize() {
	m.Version = strings.TrimSpace(m.Version)
	m.MinVersion = strings.TrimSpace(m.MinVersion)
	m.URL = strings.TrimSpace(m.URL)
	m.SHA256 = normalizeSHA256(m.SHA256)
}

func validateManifest(m manifest, opts validationOptions) error {
	if !validVersion(m.Version) {
		return fmt.Errorf("version is empty or invalid")
	}
	if m.MinVersion != "" && !validVersion(m.MinVersion) {
		return fmt.Errorf("minVersion is invalid")
	}
	if opts.requireURL {
		if err := validateInstallerURL(m.URL); err != nil {
			return err
		}
		if err := validateSHA256(m.SHA256); err != nil {
			return err
		}
	}
	return nil
}

func validateInstallerURL(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return fmt.Errorf("url is empty")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("url must be absolute HTTP(S)")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("url scheme must be http or https")
	}
	if strings.ToLower(filepath.Ext(parsed.Path)) != ".exe" {
		return fmt.Errorf("windows installer must be .exe")
	}
	return nil
}

func validateSHA256(value string) error {
	value = normalizeSHA256(value)
	if value == "" {
		return fmt.Errorf("sha256 is empty")
	}
	if len(value) != sha256.Size*2 {
		return fmt.Errorf("sha256 must be %d hex chars", sha256.Size*2)
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return fmt.Errorf("sha256 must be lowercase hex")
		}
	}
	return nil
}

func validVersion(value string) bool {
	parts := versionParts(value)
	if len(parts) == 0 {
		return false
	}
	trimmed := strings.TrimPrefix(strings.TrimSpace(value), "v")
	return leadingDigits(trimmed) != ""
}

func normalizeSHA256(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
}

func compareVersion(left, right string) int {
	leftParts := versionParts(left)
	rightParts := versionParts(right)
	max := len(leftParts)
	if len(rightParts) > max {
		max = len(rightParts)
	}
	for i := 0; i < max; i++ {
		var l, r int
		if i < len(leftParts) {
			l = leftParts[i]
		}
		if i < len(rightParts) {
			r = rightParts[i]
		}
		if l > r {
			return 1
		}
		if l < r {
			return -1
		}
	}
	return 0
}

func versionParts(version string) []int {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	chunks := strings.Split(version, ".")
	parts := make([]int, 0, len(chunks))
	for _, chunk := range chunks {
		numeric := leadingDigits(chunk)
		if numeric == "" {
			parts = append(parts, 0)
			continue
		}
		value, err := strconv.Atoi(numeric)
		if err != nil {
			parts = append(parts, 0)
			continue
		}
		parts = append(parts, value)
	}
	return parts
}

func leadingDigits(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r < '0' || r > '9' {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}
