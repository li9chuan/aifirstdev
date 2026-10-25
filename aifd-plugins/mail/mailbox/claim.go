package mailbox

import (
	"context"
	"errors"
	"fmt"

	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
)

type ClaimResult struct {
	MailIDs       []int64
	RetryAtUnixMs int64
}

// Claim pins its mailbox and uses one bounded award budget for the whole batch.
// Caller disconnection cannot discard the outcome of an already accepted award.
// A timed-out, non-idempotent award may execute again on the next client retry.
func (m *Manager) Claim(ctx context.Context, uid int64, groups []string, ids []int64) (ClaimResult, error) {
	out := ClaimResult{}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validateIDs(ids); err != nil {
		return out, err
	}
	box, release, err := m.acquire(ctx, uid)
	if err != nil {
		return out, err
	}
	defer release()
	if len(ids) == 0 {
		return out, nil
	}
	if m.Awarder == nil {
		return out, mail.ErrAwarderMissing
	}
	if fn, ok := m.Awarder.(mail.AwarderFunc); ok && fn == nil {
		return out, mail.ErrAwarderMissing
	}
	stateCtx := context.WithoutCancel(ctx)
	awardCtx, cancel := context.WithTimeout(stateCtx, m.AwardTimeout)
	defer cancel()
	var firstErr error
	for _, id := range uniqueIDs(ids) {
		if err := awardCtx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		data, retryAt, err := m.beginClaim(stateCtx, box, groups, id)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("mail %d claim: %w", id, err)
			}
			out.addRetry(retryAt)
			continue
		}
		if data == nil {
			continue
		} // Already claimed or no attachments.
		err = m.award(awardCtx, uid, data)
		if awardCtx.Err() != nil {
			err = awardCtx.Err()
		}
		changed, finishErr := m.finishClaim(stateCtx, box, id, err == nil)
		if changed {
			out.MailIDs = append(out.MailIDs, id)
		}
		if err != nil || finishErr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("mail %d award/finish: %w", id, errors.Join(err, finishErr))
			}
			out.addRetry(data.GetClaimRetryAtUnixMs())
		}
	}
	return out, firstErr
}

// An adapter panic must not leave a permanent in-flight flag behind.
func (m *Manager) award(ctx context.Context, uid int64, data *mailpb.MailData) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("mail award adapter panic: %v", recovered)
		}
	}()
	return m.Awarder.AwardMail(ctx, uid, data)
}

func (r *ClaimResult) addRetry(at int64) {
	if at > 0 && (r.RetryAtUnixMs == 0 || at < r.RetryAtUnixMs) {
		r.RetryAtUnixMs = at
	}
}

// Claim pins box for the whole batch, including RPC waits and finalization.
func (m *Manager) beginClaim(ctx context.Context, box *mailboxEntry, groups []string, id int64) (*mailpb.MailData, int64, error) {
	box.mu.Lock()
	defer box.mu.Unlock()
	e := box.mails[id]
	if e == nil || !visible(e.DbProto, groups, m.Clock.Now().Unix()) {
		return nil, 0, mail.ErrNotFound
	}
	data := e.DbProto
	if data.GetClaimed() || !hasAttachment(data) {
		return nil, 0, nil
	}
	_, active := box.claiming[id]
	if active || data.GetClaimRetryAtUnixMs() > m.Clock.Now().UnixMilli() {
		return nil, data.GetClaimRetryAtUnixMs(), mail.ErrBusy
	}
	e.Lock()
	previous := data.GetClaimRetryAtUnixMs()
	data.ClaimRetryAtUnixMs = m.Clock.Now().Add(m.ClaimCD).UnixMilli()
	if err := e.SaveUnsafe(ctx); err != nil {
		data.ClaimRetryAtUnixMs = previous
		e.Unlock()
		return nil, 0, err
	}
	e.Unlock()
	box.claiming[id] = struct{}{}
	return cloneMail(data), data.GetClaimRetryAtUnixMs(), nil
}

func (m *Manager) finishClaim(ctx context.Context, box *mailboxEntry, id int64, success bool) (bool, error) {
	box.mu.Lock()
	defer box.mu.Unlock()
	defer delete(box.claiming, id)
	e := box.mails[id]
	if e == nil {
		return false, mail.ErrNotFound
	}
	if !success {
		return false, nil
	}
	e.Lock()
	read, claimed := e.DbProto.GetRead(), e.DbProto.GetClaimed()
	e.DbProto.Read = true
	e.DbProto.Claimed = true
	err := e.SaveUnsafe(ctx)
	if err != nil {
		e.DbProto.Read = read
		e.DbProto.Claimed = claimed
	}
	e.Unlock()
	if err != nil {
		return false, err
	}
	// Clear before normalization; another in-flight mail still postpones trimming.
	delete(box.claiming, id)
	return !claimed, m.normalizeLocked(ctx, box)
}
