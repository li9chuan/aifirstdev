package contracts

import "sync"

// Report 存储 run group 计数；结构保持简单，便于合并。
type Report struct {
	mu      sync.Mutex
	Login   int64
	Send    int64
	Receive int64
	Error   int64
	Custom  map[string]int64
}

// AddLogin 按 delta 增加登录计数。
func (r *Report) AddLogin(delta int64) {
	r.add(func() { r.Login += delta })
}

// AddSend 按 delta 增加出站消息计数。
func (r *Report) AddSend(delta int64) {
	r.add(func() { r.Send += delta })
}

// AddReceive 按 delta 增加入站消息计数。
func (r *Report) AddReceive(delta int64) {
	r.add(func() { r.Receive += delta })
}

// AddError 按 delta 增加错误计数。
func (r *Report) AddError(delta int64) {
	r.add(func() { r.Error += delta })
}

// AddCustom 按 delta 增加指定名称的自定义计数。
func (r *Report) AddCustom(name string, delta int64) {
	r.add(func() {
		if r.Custom == nil {
			r.Custom = make(map[string]int64)
		}
		r.Custom[name] += delta
	})
}

// Snapshot 返回当前 report 计数的无锁副本。
func (r *Report) Snapshot() ReportSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	custom := make(map[string]int64, len(r.Custom))
	for k, v := range r.Custom {
		custom[k] = v
	}
	return ReportSnapshot{
		Login:   r.Login,
		Send:    r.Send,
		Receive: r.Receive,
		Error:   r.Error,
		Custom:  custom,
	}
}

func (r *Report) add(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn()
}

// ReportSnapshot 是 Report 的无锁副本。
type ReportSnapshot struct {
	Login   int64
	Send    int64
	Receive int64
	Error   int64
	Custom  map[string]int64
}
