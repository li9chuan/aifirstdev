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

// StatusFromMessage converts a status response header into a typed error.
func StatusFromMessage(msg Message) error {
	if len(msg.Header) == 0 {
		return nil
	}
	code := msg.Header[HeaderStatus]
	if code == "" {
		return nil
	}
	return &StatusError{
		Code:    code,
		Message: msg.Header[HeaderErrorMessage],
	}
}
