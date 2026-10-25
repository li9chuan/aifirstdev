package mail

import (
	"context"
	"errors"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
)

// Clock provides testable wall-clock access for mail lifecycle decisions.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function into Clock.
type ClockFunc func() time.Time

// Now returns the wrapped function's time, or time.Now when the function is nil.
func (f ClockFunc) Now() time.Time {
	if f == nil {
		return time.Now()
	}
	return f()
}

// SystemClock is the production wall-clock implementation.
type SystemClock struct{}

// Now returns the current local wall-clock time.
func (SystemClock) Now() time.Time {
	return time.Now()
}

// IDGenerator allocates globally unique mail IDs.
type IDGenerator interface {
	NextID() (int64, error)
}

// MailPushPort delivers a newly stored personal mail to online client sessions.
// Delivery is best effort: a push failure must not roll back accepted mail.
type MailPushPort interface {
	PushMailReceived(context.Context, int64, *mailpb.MseMailReceived) error
}

// MailPushPortFunc adapts a function into MailPushPort.
type MailPushPortFunc func(context.Context, int64, *mailpb.MseMailReceived) error

// PushMailReceived calls the wrapped function.
func (f MailPushPortFunc) PushMailReceived(ctx context.Context, rootID int64, event *mailpb.MseMailReceived) error {
	if f == nil {
		return nil
	}
	return f(ctx, rootID, event)
}

// Awarder executes one ordinary award operation and must respect the context deadline.
// Nil means business success, not merely that a message was sent. Mail does not retry
// this call automatically and does not require recipient-side deduplication.
type Awarder interface {
	AwardMail(context.Context, int64, *mailpb.MailData) error
}

// AwarderFunc adapts a function into Awarder.
type AwarderFunc func(context.Context, int64, *mailpb.MailData) error

// AwardMail calls the wrapped function.
func (f AwarderFunc) AwardMail(ctx context.Context, rootID int64, data *mailpb.MailData) error {
	if f == nil {
		return ErrAwarderMissing
	}
	return f(ctx, rootID, data)
}

// ErrAwarderMissing leaves both claim state and CD unchanged.
var ErrAwarderMissing = errors.New("mail awarder is missing")

// TemplateValidator allows a business project to validate mail templates without coupling the plugin to config tables.
type TemplateValidator interface {
	ValidateMail(context.Context, *mailpb.MailBase) error
}

// TemplateValidatorFunc adapts a function into TemplateValidator.
type TemplateValidatorFunc func(context.Context, *mailpb.MailBase) error

// ValidateMail calls the wrapped function.
func (f TemplateValidatorFunc) ValidateMail(ctx context.Context, base *mailpb.MailBase) error {
	if f == nil {
		return nil
	}
	return f(ctx, base)
}
