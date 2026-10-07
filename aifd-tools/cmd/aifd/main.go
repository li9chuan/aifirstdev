package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-tools/internal/devservice"
	"github.com/li9chuan/aifirstdev/aifd-tools/internal/installer"
	"github.com/li9chuan/aifirstdev/aifd-tools/internal/lockfile"
	"github.com/li9chuan/aifirstdev/aifd-tools/internal/toolchain"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, ""))
}

func run(args []string, stdout io.Writer, stderr io.Writer, cwd string) int {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "get cwd: %v\n", err)
			return 1
		}
	}
	if len(args) == 0 {
		usage(stdout)
		return 0
	}
	switch args[0] {
	case "update":
		return update(args[1:], stdout, stderr)
	case "tools":
		return tools(args[1:], stdout, stderr, cwd)
	case "proto":
		return protoCmd(args[1:], stdout, stderr, cwd)
	case "config":
		return configCmd(args[1:], stdout, stderr, cwd)
	case "dev":
		return dev(args[1:], stdout, stderr, cwd)
	default:
		usage(stderr)
		return 2
	}
}

func update(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) > 1 || (len(args) == 1 && args[0] != "--check") {
		fmt.Fprintln(stderr, "usage: aifd update [--check]; AIFD is versioned as one framework, without per-module updates")
		return 2
	}
	fmt.Fprintln(stdout, "aifd update: reserved; no remote check or update was performed")
	fmt.Fprintln(stdout, "upgrade the framework in your project with: go get github.com/li9chuan/aifirstdev@<version>")
	return 0
}

func tools(args []string, stdout io.Writer, stderr io.Writer, cwd string) int {
	if len(args) == 0 {
		toolsUsage(stdout)
		return 0
	}
	switch args[0] {
	case "doctor":
		return toolsDoctor(args[1:], stdout, stderr, cwd)
	case "path":
		return toolsPath(args[1:], stdout, stderr, cwd)
	case "install":
		return toolsInstall(args[1:], stdout, stderr, cwd)
	default:
		fmt.Fprintf(stderr, "unknown tools command %q\n", args[0])
		return 2
	}
}

func toolsDoctor(args []string, stdout io.Writer, stderr io.Writer, cwd string) int {
	opts, code := parseToolOptions("tools doctor", args, stderr, cwd)
	if code != 0 {
		return code
	}
	results := opts.resolver.ResolveAll(context.Background(), true)
	printToolResults(stdout, results)
	if doctorFailed(results) {
		return 1
	}
	return 0
}

func toolsPath(args []string, stdout io.Writer, stderr io.Writer, cwd string) int {
	opts, code := parseToolOptions("tools path", args, stderr, cwd)
	if code != 0 {
		return code
	}
	results := opts.resolver.ResolveAll(context.Background(), false)
	printToolResults(stdout, results)
	for _, result := range results {
		if result.Optional {
			continue
		}
		if result.Missing {
			return 1
		}
	}
	return 0
}

func toolsInstall(args []string, stdout io.Writer, stderr io.Writer, cwd string) int {
	fs := newFlagSet("tools install", stderr)
	var opts commonToolOptions
	var tools multiFlag
	var manifestPath string
	var force, dryRun bool
	var timeout time.Duration
	bindCommonToolFlags(fs, &opts)
	fs.Var(&tools, "tool", "tool to install; repeatable")
	fs.StringVar(&manifestPath, "manifest", "", "install manifest json path")
	fs.BoolVar(&force, "force", false, "replace existing installed tool")
	fs.BoolVar(&dryRun, "dry-run", false, "print install plan without installing files")
	fs.DurationVar(&timeout, "timeout", 5*time.Minute, "download/install timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	tools = append(tools, fs.Args()...)
	resolved := buildResolver(opts, cwd)
	manifest := installer.DefaultManifest()
	if manifestPath != "" {
		loaded, err := installer.LoadManifestFile(manifestPath)
		if err != nil {
			fmt.Fprintf(stderr, "read manifest: %v\n", err)
			return 1
		}
		manifest = loaded
	}
	goExe := opts.goPath
	if goExe == "" {
		if spec, ok := toolchain.SpecByName(toolchain.ToolGo); ok {
			result := resolved.resolver.Resolve(context.Background(), spec, false)
			if !result.Missing && !result.Unsupported && result.Path != "" {
				goExe = result.Path
			}
		}
	}
	lock := loadLock(resolved.projectDir)
	installCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	nextLock, results := installer.Install(installCtx, installer.Options{
		ProjectDir: resolved.projectDir,
		Lock:       lock,
		Manifest:   manifest,
		Tools:      tools,
		Force:      force,
		DryRun:     dryRun,
		Timeout:    timeout,
		GoExe:      goExe,
	})
	printInstallResults(stdout, results)
	if !dryRun && installResultsOK(results) {
		if err := writeLock(resolved.projectDir, nextLock); err != nil {
			fmt.Fprintf(stderr, "write aifd.lock: %v\n", err)
			return 1
		}
	}
	if !installResultsOK(results) {
		return 1
	}
	return 0
}

func protoCmd(args []string, stdout io.Writer, stderr io.Writer, cwd string) int {
	if len(args) == 0 {
		protoUsage(stdout)
		return 0
	}
	switch args[0] {
	case "gen":
		return protoGen(args[1:], stdout, stderr, cwd)
	case "check", "diff":
		fmt.Fprintf(stdout, "aifd proto %s: planned\n", args[0])
		return 0
	default:
		fmt.Fprintf(stderr, "unknown proto command %q\n", args[0])
		return 2
	}
}

func protoGen(args []string, stdout io.Writer, stderr io.Writer, cwd string) int {
	fs := newFlagSet("proto gen", stderr)
	var opts commonToolOptions
	var protoPaths, includes, extras multiFlag
	var goOut, out string
	bindCommonToolFlags(fs, &opts)
	fs.Var(&protoPaths, "proto_path", "protoc import path; repeatable")
	fs.StringVar(&goOut, "go_out", "", "protoc go_out value")
	fs.StringVar(&out, "out", "", "descriptor_set_out path")
	fs.Var(&includes, "include", "proto file to compile; repeatable")
	fs.Var(&extras, "extra", "extra argument passed to protoc; repeatable")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	includes = append(includes, fs.Args()...)
	resolved := buildResolver(opts, cwd)
	protocSpec, _ := toolchain.SpecByName(toolchain.ToolProtoc)
	result := resolved.resolver.Resolve(context.Background(), protocSpec, false)
	if result.Missing {
		fmt.Fprintln(stderr, result.Error)
		return 1
	}

	cmdArgs, err := loadArgsFile(resolved.projectDir, filepath.Join(".aifd", "proto.args"))
	if err != nil {
		fmt.Fprintf(stderr, "read proto args: %v\n", err)
		return 1
	}
	if len(protoPaths) > 0 || goOut != "" || out != "" || len(includes) > 0 || len(extras) > 0 {
		cmdArgs = cmdArgs[:0]
		for _, path := range protoPaths {
			cmdArgs = append(cmdArgs, "--proto_path="+path)
		}
		if goOut != "" {
			cmdArgs = append(cmdArgs, "--go_out="+goOut)
		}
		if out != "" {
			cmdArgs = append(cmdArgs, "--descriptor_set_out="+out)
		}
		cmdArgs = append(cmdArgs, extras...)
		cmdArgs = append(cmdArgs, includes...)
	}
	if len(cmdArgs) == 0 {
		fmt.Fprintln(stderr, "proto gen has no inputs; pass --include or create .aifd/proto.args")
		return 2
	}
	cmdEnv := os.Environ()
	if includeDir := toolIncludeDir(result.Path); includeDir != "" && dirExists(includeDir) && !hasProtoPath(cmdArgs, includeDir) {
		cmdArgs = append(cmdArgs, "--proto_path="+includeDir)
	}
	if protoArgsNeedGoPlugin(goOut, cmdArgs) {
		pluginSpec, _ := toolchain.SpecByName(toolchain.ToolProtocGenGo)
		plugin := resolved.resolver.Resolve(context.Background(), pluginSpec, false)
		if plugin.Missing || plugin.Unsupported {
			fmt.Fprintln(stderr, plugin.Error)
			return 1
		}
		cmdEnv = prependPath(cmdEnv, filepath.Dir(plugin.Path))
	}
	return execToolWithEnv(stdout, stderr, resolved.projectDir, result.Path, cmdArgs, cmdEnv)
}

func configCmd(args []string, stdout io.Writer, stderr io.Writer, cwd string) int {
	if len(args) == 0 {
		configUsage(stdout)
		return 0
	}
	switch args[0] {
	case "gen":
		return configGen(args[1:], stdout, stderr, cwd)
	case "check", "diff", "hotreload":
		fmt.Fprintf(stdout, "aifd config %s: planned\n", args[0])
		return 0
	default:
		fmt.Fprintf(stderr, "unknown config command %q\n", args[0])
		return 2
	}
}

func configGen(args []string, stdout io.Writer, stderr io.Writer, cwd string) int {
	fs := newFlagSet("config gen", stderr)
	var opts commonToolOptions
	var conf, out, target string
	var extras multiFlag
	bindCommonToolFlags(fs, &opts)
	fs.StringVar(&conf, "conf", "", "Luban config path")
	fs.StringVar(&out, "out", "", "Luban output path")
	fs.StringVar(&target, "target", "", "Luban target")
	fs.Var(&extras, "extra", "extra argument passed to Luban; repeatable")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	resolved := buildResolver(opts, cwd)
	lubanSpec, _ := toolchain.SpecByName(toolchain.ToolLuban)
	result := resolved.resolver.Resolve(context.Background(), lubanSpec, false)
	if result.Missing {
		fmt.Fprintln(stderr, result.Error)
		return 1
	}

	cmdArgs, err := loadArgsFile(resolved.projectDir, filepath.Join(".aifd", "config.args"))
	if err != nil {
		fmt.Fprintf(stderr, "read config args: %v\n", err)
		return 1
	}
	if conf != "" || out != "" || target != "" || len(extras) > 0 || len(fs.Args()) > 0 {
		cmdArgs = cmdArgs[:0]
		if conf != "" {
			cmdArgs = append(cmdArgs, "--conf="+conf)
		}
		if out != "" {
			cmdArgs = append(cmdArgs, "--out="+out)
		}
		if target != "" {
			cmdArgs = append(cmdArgs, "--target="+target)
		}
		cmdArgs = append(cmdArgs, extras...)
		cmdArgs = append(cmdArgs, fs.Args()...)
	}
	if len(cmdArgs) == 0 {
		fmt.Fprintln(stderr, "config gen has no inputs; pass --conf/--extra or create .aifd/config.args")
		return 2
	}
	return execTool(stdout, stderr, resolved.projectDir, result.Path, cmdArgs)
}

func dev(args []string, stdout io.Writer, stderr io.Writer, cwd string) int {
	if len(args) == 0 {
		devUsage(stdout)
		return 0
	}
	switch args[0] {
	case "up":
		return devUp(args[1:], stdout, stderr, cwd)
	case "down":
		return devDown(args[1:], stdout, stderr, cwd)
	case "status":
		return devStatus(args[1:], stdout, stderr, cwd)
	default:
		fmt.Fprintf(stderr, "unknown dev command %q\n", args[0])
		return 2
	}
}

func devUp(args []string, stdout io.Writer, stderr io.Writer, cwd string) int {
	opts, service, code := parseDevOptions("dev up", args, stderr, cwd)
	if code != 0 {
		return code
	}
	var failures []string
	for _, target := range selectedServices(service) {
		spec := devServiceSpec(target)
		result := opts.resolver.Resolve(context.Background(), spec.toolSpec, false)
		if result.Missing {
			failures = append(failures, target+": "+result.Error)
			continue
		}
		if result.Unsupported {
			failures = append(failures, target+": "+result.Error)
			continue
		}
		status, err := devservice.Start(target, result.Path, spec.args, opts.projectDir)
		if err != nil {
			failures = append(failures, target+": "+err.Error())
			continue
		}
		fmt.Fprintf(stdout, "%s started pid=%d log=%s\n", target, status.PID, status.LogFile)
	}
	if len(failures) > 0 {
		for _, failure := range failures {
			fmt.Fprintln(stderr, failure)
		}
		return 1
	}
	return 0
}

func devDown(args []string, stdout io.Writer, stderr io.Writer, cwd string) int {
	opts, service, code := parseDevOptions("dev down", args, stderr, cwd)
	if code != 0 {
		return code
	}
	for _, target := range selectedServices(service) {
		status, err := devservice.Stop(target, opts.projectDir)
		if err != nil {
			fmt.Fprintf(stderr, "%s stop error: %v\n", target, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s stopped pid=%d\n", target, status.PID)
	}
	return 0
}

func devStatus(args []string, stdout io.Writer, stderr io.Writer, cwd string) int {
	opts, service, code := parseDevOptions("dev status", args, stderr, cwd)
	if code != 0 {
		return code
	}
	for _, target := range selectedServices(service) {
		status := devservice.GetStatus(target, opts.projectDir)
		state := "stopped"
		if status.Running {
			state = "running"
		}
		fmt.Fprintf(stdout, "%s %s pid=%d log=%s\n", target, state, status.PID, status.LogFile)
	}
	return 0
}

type commonToolOptions struct {
	project     string
	goPath      string
	protoc      string
	protocGenGo string
	luban       string
	redis       string
	wails       string
	makensis    string
}

type resolvedOptions struct {
	projectDir string
	resolver   toolchain.Resolver
}

func parseToolOptions(name string, args []string, stderr io.Writer, cwd string) (resolvedOptions, int) {
	fs := newFlagSet(name, stderr)
	var opts commonToolOptions
	bindCommonToolFlags(fs, &opts)
	if err := fs.Parse(args); err != nil {
		return resolvedOptions{}, 2
	}
	return buildResolver(opts, cwd), 0
}

func parseDevOptions(name string, args []string, stderr io.Writer, cwd string) (resolvedOptions, string, int) {
	fs := newFlagSet(name, stderr)
	var opts commonToolOptions
	service := "all"
	bindCommonToolFlags(fs, &opts)
	fs.StringVar(&service, "service", "all", "service to manage: all, redis")
	if err := fs.Parse(args); err != nil {
		return resolvedOptions{}, "", 2
	}
	if service != "all" && service != "redis" {
		fmt.Fprintf(stderr, "unknown service %q\n", service)
		return resolvedOptions{}, "", 2
	}
	return buildResolver(opts, cwd), service, 0
}

func bindCommonToolFlags(fs *flag.FlagSet, opts *commonToolOptions) {
	fs.StringVar(&opts.project, "project", "", "project root")
	fs.StringVar(&opts.goPath, "go", "", "go executable")
	fs.StringVar(&opts.protoc, "protoc", "", "protoc executable")
	fs.StringVar(&opts.protocGenGo, "protoc-gen-go", "", "protoc-gen-go executable")
	fs.StringVar(&opts.luban, "luban", "", "Luban executable")
	fs.StringVar(&opts.redis, "redis-server", "", "redis-server executable")
	fs.StringVar(&opts.wails, "wails", "", "wails3 executable")
	fs.StringVar(&opts.makensis, "makensis", "", "makensis executable")
}

func buildResolver(opts commonToolOptions, cwd string) resolvedOptions {
	projectDir := opts.project
	if projectDir == "" {
		projectDir = findProjectRoot(cwd)
	}
	if abs, err := filepath.Abs(projectDir); err == nil {
		projectDir = abs
	}
	lock := loadLock(projectDir)
	overrides := map[string]string{}
	if opts.goPath != "" {
		overrides[toolchain.ToolGo] = opts.goPath
	}
	if opts.protoc != "" {
		overrides[toolchain.ToolProtoc] = opts.protoc
	}
	if opts.protocGenGo != "" {
		overrides[toolchain.ToolProtocGenGo] = opts.protocGenGo
	}
	if opts.luban != "" {
		overrides[toolchain.ToolLuban] = opts.luban
	}
	if opts.redis != "" {
		overrides[toolchain.ToolRedis] = opts.redis
	}
	if opts.wails != "" {
		overrides[toolchain.ToolWails] = opts.wails
	}
	if opts.makensis != "" {
		overrides[toolchain.ToolNSIS] = opts.makensis
	}
	return resolvedOptions{
		projectDir: projectDir,
		resolver: toolchain.Resolver{
			ProjectDir: projectDir,
			Lock:       lock,
			Overrides:  overrides,
		},
	}
}

func loadLock(projectDir string) lockfile.Lock {
	f, err := os.Open(filepath.Join(projectDir, "aifd.lock"))
	if err != nil {
		lock, _ := lockfile.Parse(strings.NewReader(""))
		return lock
	}
	defer f.Close()
	lock, err := lockfile.Parse(f)
	if err != nil {
		empty, _ := lockfile.Parse(strings.NewReader(""))
		return empty
	}
	return lock
}

func writeLock(projectDir string, lock lockfile.Lock) error {
	path := filepath.Join(projectDir, "aifd.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(lock.Format()), 0644)
}

func findProjectRoot(cwd string) string {
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "aifd.lock")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			return cwd
		}
		dir = next
	}
}

func execTool(stdout io.Writer, stderr io.Writer, projectDir string, exe string, args []string) int {
	return execToolWithEnv(stdout, stderr, projectDir, exe, args, nil)
}

func execToolWithEnv(stdout io.Writer, stderr io.Writer, projectDir string, exe string, args []string, env []string) int {
	cmd := exec.CommandContext(context.Background(), exe, args...)
	cmd.Dir = projectDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if env != nil {
		cmd.Env = env
	}
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stderr, "%s failed: %v\n", filepath.Base(exe), err)
		return 1
	}
	return 0
}

func printInstallResults(w io.Writer, results []installer.Result) {
	for _, result := range results {
		fmt.Fprintf(w, "%s\t%s\tversion=%s\ttarget=%s", result.Status, result.Tool, result.Version, result.Target)
		if result.URL != "" {
			fmt.Fprintf(w, "\turl=%s", result.URL)
		}
		if result.Message != "" {
			fmt.Fprintf(w, "\t%s", result.Message)
		}
		fmt.Fprintln(w)
	}
}

func installResultsOK(results []installer.Result) bool {
	for _, result := range results {
		if result.Err != nil {
			return false
		}
	}
	return true
}

func printToolResults(w io.Writer, results []toolchain.Result) {
	for _, result := range results {
		state := "ok"
		if result.Unsupported {
			state = "unsupported"
		} else if result.Missing {
			state = "missing"
			if result.Optional {
				state = "optional-missing"
			}
		} else if result.Error != "" {
			state = "warn"
			if result.Optional {
				state = "optional-warn"
			}
		}
		fmt.Fprintf(w, "%s\t%s\tpath=%s\tsource=%s", state, result.Name, result.Path, result.Source)
		if result.ExpectedVersion != "" {
			fmt.Fprintf(w, "\texpected=%s", result.ExpectedVersion)
		}
		if result.DetectedVersion != "" {
			fmt.Fprintf(w, "\tdetected=%s", result.DetectedVersion)
		}
		if result.Error != "" {
			fmt.Fprintf(w, "\t%s", result.Error)
		}
		fmt.Fprintln(w)
	}
}

func doctorFailed(results []toolchain.Result) bool {
	for _, result := range results {
		if result.Optional {
			continue
		}
		if result.Missing {
			return true
		}
		if result.Unsupported {
			continue
		}
		if strings.Contains(result.Error, "version mismatch") {
			return true
		}
	}
	return false
}

func loadArgsFile(projectDir string, rel string) ([]string, error) {
	path := filepath.Join(projectDir, rel)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var args []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		args = append(args, strings.Fields(line)...)
	}
	return args, scanner.Err()
}

func protoArgsNeedGoPlugin(goOut string, args []string) bool {
	if goOut != "" {
		return true
	}
	for _, arg := range args {
		if arg == "--go_out" || strings.HasPrefix(arg, "--go_out=") {
			return true
		}
	}
	return false
}

func hasProtoPath(args []string, path string) bool {
	for _, arg := range args {
		if arg == "--proto_path="+path || arg == "-I"+path {
			return true
		}
	}
	return false
}

func toolIncludeDir(exe string) string {
	binDir := filepath.Dir(exe)
	if filepath.Base(binDir) != "bin" {
		return ""
	}
	return filepath.Join(filepath.Dir(binDir), "include")
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func prependPath(env []string, dir string) []string {
	if dir == "" {
		return env
	}
	key := "PATH"
	sep := string(os.PathListSeparator)
	for i, item := range env {
		name, value, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(name, key) {
			env[i] = name + "=" + dir + sep + value
			return env
		}
	}
	return append(env, key+"="+dir)
}

type devSpec struct {
	toolSpec toolchain.Spec
	args     []string
}

func devServiceSpec(name string) devSpec {
	switch name {
	case "redis":
		spec, _ := toolchain.SpecByName(toolchain.ToolRedis)
		return devSpec{toolSpec: spec}
	default:
		panic("unknown dev service")
	}
}

func selectedServices(service string) []string {
	switch service {
	case "redis":
		return []string{"redis"}
	default:
		return []string{"redis"}
	}
}

type multiFlag []string

// String 将已提供的多个 flag 值格式化为 flag.Value 需要的字符串。
func (m *multiFlag) String() string {
	return strings.Join(*m, ",")
}

// Set 追加一个重复出现的 flag 值。
func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: aifd <command> [args]")
	fmt.Fprintln(w, "commands: update, tools, proto, config, dev")
	toolsUsage(w)
	protoUsage(w)
	configUsage(w)
	devUsage(w)
}

func toolsUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: aifd tools [doctor|path|install] [--project dir] [--protoc path] [--protoc-gen-go path] [--luban path] [--redis-server path] [--wails path] [--makensis path]")
}

func protoUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: aifd proto [gen|check|diff]")
}

func configUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: aifd config [gen|check|diff|hotreload]")
}

func devUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: aifd dev [up|down|status] [--project dir] [--service all|redis]")
}
