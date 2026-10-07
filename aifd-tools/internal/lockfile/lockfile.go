package lockfile

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Lock 保存从 aifd.lock 解析出的模块版本和工具版本。
type Lock struct {
	// Versions 保留旧版调用方使用的扁平视图。
	Versions map[string]string
	Modules  map[string]string
	Tools    map[string]string
}

// Parse 读取简单的 name-version lockfile 格式。
func Parse(r io.Reader) (Lock, error) {
	lock := Lock{
		Versions: make(map[string]string),
		Modules:  make(map[string]string),
		Tools:    make(map[string]string),
	}
	scanner := bufio.NewScanner(r)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, version, ok := strings.Cut(line, ":")
		if !ok {
			return Lock{}, fmt.Errorf("line %d: missing ':'", lineNo)
		}
		name = strings.TrimSpace(name)
		version = strings.TrimSpace(version)
		if name == "" || version == "" {
			return Lock{}, fmt.Errorf("line %d: empty name or version", lineNo)
		}
		lock.Versions[name] = version
		if toolName, ok := strings.CutPrefix(name, "tools."); ok {
			lock.Tools[toolName] = version
		} else {
			lock.Modules[name] = version
		}
	}
	if err := scanner.Err(); err != nil {
		return Lock{}, err
	}
	return lock, nil
}

// Format 按稳定顺序序列化模块和工具版本。
func (l Lock) Format() string {
	names := make([]string, 0, len(l.Modules))
	for name := range l.Modules {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(l.Modules[name])
		b.WriteString("\n")
	}

	tools := make([]string, 0, len(l.Tools))
	for name := range l.Tools {
		tools = append(tools, name)
	}
	sort.Strings(tools)
	for _, name := range tools {
		b.WriteString("tools.")
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(l.Tools[name])
		b.WriteString("\n")
	}
	return b.String()
}

// ToolVersion 返回指定工具名锁定的版本。
func (l Lock) ToolVersion(name string) string {
	return l.Tools[name]
}

// SetToolVersion 同时在旧版扁平表和工具版本表中记录工具版本。
func (l *Lock) SetToolVersion(name string, version string) {
	if l.Versions == nil {
		l.Versions = make(map[string]string)
	}
	if l.Tools == nil {
		l.Tools = make(map[string]string)
	}
	key := "tools." + name
	l.Versions[key] = version
	l.Tools[name] = version
}
