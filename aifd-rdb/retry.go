package rdb

import (
	"context"
	"errors"
	"net"
	"strings"
)

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	nonRetryable := []string{
		"wrongtype",
		"noauth",
		"noperm",
		"oom",
		"invalid",
		"marshal",
	}
	for _, pattern := range nonRetryable {
		if strings.Contains(msg, pattern) {
			return false
		}
	}
	retryable := []string{
		"dial",
		"connection refused",
		"connection reset",
		"connection closed",
		"client is closed",
		"i/o timeout",
		"broken pipe",
		"no route to host",
		"network is unreachable",
		"loading",
		"busy",
		"tryagain",
		"clusterdown",
	}
	for _, pattern := range retryable {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}
