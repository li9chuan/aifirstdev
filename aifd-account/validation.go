package account

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"github.com/li9chuan/aifirstdev/aifd-account/credential"
	"math"
	"strconv"
	"time"
)

const defaultMaxIdentities = 8

func encodeFields(fields ...string) []byte {
	var out []byte
	for _, field := range fields {
		out = binary.BigEndian.AppendUint32(out, uint32(len(field)))
		out = append(out, field...)
	}
	return out
}

func loginRefFromPrincipal(key []byte, principal credential.Principal) (LoginRef, error) {
	if !credential.ValidID(principal.AuthorityID) || len(principal.Subject) == 0 || len(principal.Subject) > credential.MaxProofBytes {
		return LoginRef{}, ErrInvalidCredential
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(encodeFields("aifd-login-v1", principal.AuthorityID, principal.Subject))
	ref := LoginRef{AuthorityID: principal.AuthorityID}
	copy(ref.SubjectDigest[:], mac.Sum(nil))
	return ref, nil
}

func validateLoginRef(ref LoginRef) error {
	if !credential.ValidID(ref.AuthorityID) {
		return ErrInvalidCredential
	}
	return nil
}

func validSessionRef(ref SessionRef) bool { return ref.AccountID > 0 && validSessionID(ref.SessionID) }
func validSessionID(id string) bool {
	if len(id) != 32 {
		return false
	}
	raw, err := hex.DecodeString(id)
	return err == nil && hex.EncodeToString(raw) == id
}

func validateSnapshot(a AccountSnapshot) error {
	if a.AccountID <= 0 || (a.Status != AccountStatusActive && a.Status != AccountStatusDisabled) || a.IdentityRevision == 0 || a.SessionEpoch == 0 || len(a.Identities) == 0 {
		return ErrCorruptState
	}
	seen := make(map[LoginRef]struct{}, len(a.Identities))
	for _, ref := range a.Identities {
		if validateLoginRef(ref) != nil {
			return ErrCorruptState
		}
		if _, ok := seen[ref]; ok {
			return ErrCorruptState
		}
		seen[ref] = struct{}{}
	}
	return nil
}

func copySnapshot(a AccountSnapshot) AccountSnapshot {
	a.Identities = append([]LoginRef(nil), a.Identities...)
	return a
}
func identityIndex(a AccountSnapshot, ref LoginRef) int {
	for i, identity := range a.Identities {
		if identity == ref {
			return i
		}
	}
	return -1
}

func validateStoredSession(a AccountSnapshot, session LoginSession, ref SessionRef, now time.Time) error {
	if validateSnapshot(a) != nil || !validSessionRef(session.Ref) || session.SessionEpoch == 0 || session.ExpiresAt.IsZero() {
		return ErrCorruptState
	}
	if session.Ref != ref || session.Ref.AccountID != a.AccountID || !now.Before(session.ExpiresAt) || session.SessionEpoch != a.SessionEpoch {
		return ErrSessionInvalid
	}
	if a.Status != AccountStatusActive {
		return ErrAccountDisabled
	}
	return nil
}

func nextVersion(version uint64) (uint64, error) {
	if version == 0 || version == math.MaxUint64 {
		return 0, ErrEpochConflict
	}
	return version + 1, nil
}
func formatAccountID(id int64) string { return strconv.FormatInt(id, 10) }
