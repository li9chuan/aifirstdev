// Package other provides a distinct package's event for name-conflict tests.
package other

type JobFinished struct {
	Result string `stat:"result"`
}
