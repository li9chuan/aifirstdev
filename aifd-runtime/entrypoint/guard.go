package entrypoint

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// CrashInfo 描述进程级 panic 或主动触发的崩溃信息。
type CrashInfo struct {
	Time         time.Time
	Reason       string
	Source       string
	PanicValue   any
	PID          int
	CurrentStack []byte
	AllStacks    []byte
}

// CrashHandler 处理进程内首次捕获到的崩溃。
type CrashHandler func(CrashInfo)

var (
	crashing     atomic.Bool
	crashHandler CrashHandler
	crashMu      sync.RWMutex
)

// SetCrashHandler 设置进程级崩溃处理器。
func SetCrashHandler(handler CrashHandler) {
	crashMu.Lock()
	crashHandler = handler
	crashMu.Unlock()
}

// Go 启动带 panic 保护的 goroutine。
func Go(name string, fn func()) {
	go RunGuarded(name, fn)
}

// RunGuarded 在当前 goroutine 中执行 fn，并把 panic 路由到崩溃处理器。
func RunGuarded(name string, fn func()) {
	defer Recover(name)
	fn()
}

// Recover 可由调用方 defer，用于统一捕获 goroutine panic。
func Recover(source string) {
	if value := recover(); value != nil {
		if !TriggerPanicCrash(source, value) && currentCrashHandler() != nil {
			select {}
		}
		panic(value)
	}
}

// TriggerPanicCrash 记录首次 panic，并同步执行崩溃处理器。
func TriggerPanicCrash(source string, value any) bool {
	info := CrashInfo{
		Time:         time.Now(),
		Reason:       "panic",
		Source:       source,
		PanicValue:   value,
		PID:          os.Getpid(),
		CurrentStack: debug.Stack(),
		AllStacks:    allGoroutineStacks(),
	}
	return triggerCrash(info)
}

func triggerCrash(info CrashInfo) bool {
	if !crashing.CompareAndSwap(false, true) {
		return false
	}
	handler := currentCrashHandler()
	if handler == nil {
		fmt.Fprintf(os.Stderr, "entrypoint crash reason=%s source=%s panic=%v\n%s\n",
			info.Reason, info.Source, info.PanicValue, info.AllStacks)
		os.Exit(2)
	}
	handler(info)
	return true
}

func currentCrashHandler() CrashHandler {
	crashMu.RLock()
	defer crashMu.RUnlock()
	return crashHandler
}

func allGoroutineStacks() []byte {
	size := 1 << 20
	for {
		buf := make([]byte, size)
		n := runtime.Stack(buf, true)
		if n < size {
			return buf[:n]
		}
		size *= 2
	}
}
