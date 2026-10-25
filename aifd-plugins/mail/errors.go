package mail

import "errors"

var (
	ErrInvalid       = errors.New("invalid mail request")
	ErrNotFound      = errors.New("mail expired, unavailable or deleted")
	ErrBusy          = errors.New("mail claim is in progress or cooling down")
	ErrNotReady      = errors.New("mail owner is not recovered")
	ErrClosed        = errors.New("mail owner is closing")
	ErrCacheCapacity = errors.New("mail cache has no safely evictable mailbox")
)
