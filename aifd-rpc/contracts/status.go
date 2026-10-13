package contracts

import "fmt"

// StatusError reports a framework status response returned by a remote handler.
type StatusError struct {
	Code    string
	Message string
}

func (e *StatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return e.Code
	}
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// StatusFromMessage converts a status header or a legacy RPCError reply into a typed error.
func StatusFromMessage(msg Message) error {
	code := msg.Header[HeaderStatus]
	if code == "" {
		if msg.Name == "RPCError" {
			return &StatusError{Code: "INTERNAL", Message: "rpc request failed"}
		}
		return nil
	}
	return &StatusError{
		Code:    code,
		Message: msg.Header[HeaderErrorMessage],
	}
}
