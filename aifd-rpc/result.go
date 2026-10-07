package rpc

import "fmt"

const (
	CodeOK                = "OK"
	CodeInvalid           = "INVALID"
	CodeDecode            = "DECODE"
	CodeInternal          = "INTERNAL"
	CodeTransport         = "TRANSPORT"
	CodeTimeout           = "TIMEOUT"
	CodeUnsupported       = "UNSUPPORTED"
	CodeDuplicateResponse = "DUPLICATE_RESPONSE"
)

// Result 表示 Session 动作的执行状态；它不实现 Go error。
type Result struct {
	ok      bool
	code    string
	message string
}

// OK 表示动作是否成功。
func (r Result) OK() bool {
	return r.ok
}

// Code 返回框架状态码。
func (r Result) Code() string {
	if r.ok {
		return CodeOK
	}
	if r.code == "" {
		return CodeInternal
	}
	return r.code
}

// Message 返回状态描述。
func (r Result) Message() string {
	return r.message
}

// String 返回适合日志记录的状态文本。
func (r Result) String() string {
	if r.ok {
		return CodeOK
	}
	if r.message == "" {
		return r.Code()
	}
	return fmt.Sprintf("%s: %s", r.Code(), r.message)
}

func okResult() Result {
	return Result{ok: true, code: CodeOK}
}

func failResult(code, message string) Result {
	if code == "" {
		code = CodeInternal
	}
	return Result{code: code, message: message}
}
