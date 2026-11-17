package contracts

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
	"google.golang.org/protobuf/proto"
)

// HeaderAuthContext is reserved for trusted proxy-to-service metadata.
const HeaderAuthContext = "proxyAuthContext"

// MaxAuthContextBytes bounds the encoded internal header before decoding.
const MaxAuthContextBytes = 4 << 10

var (
	ErrAuthContextInvalid = errors.New("AUTH_CONTEXT_INVALID")
	ErrAuthExpired        = errors.New("AUTH_EXPIRED")
)

// Validate checks the authentication snapshot; it does not query an authority.
func (a AuthContext) Validate(now time.Time) error {
	if !validAuthValue(a.BackendID, 128, false) || !validAuthValue(a.Account, 512, false) || !validAuthValue(a.SessionID, 256, true) || a.ValidUntil.IsZero() || a.ValidUntil.Unix() <= 0 {
		return ErrAuthContextInvalid
	}
	if !now.Before(a.ValidUntil) {
		return ErrAuthExpired
	}
	return nil
}

// GetAuthContext reads a snapshot injected by the gateway after the backend's
// trusted-source and connection-lease gates. Header bytes alone prove no origin.
func GetAuthContext(caller ClientCaller) (AuthContext, error) {
	if _, err := ClientSessionRef(caller); err != nil {
		return AuthContext{}, ErrAuthContextInvalid
	}
	encoded := caller.Header(HeaderAuthContext)
	if len(encoded) == 0 || len(encoded) > MaxAuthContextBytes {
		return AuthContext{}, ErrAuthContextInvalid
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return AuthContext{}, ErrAuthContextInvalid
	}
	var pb proxypb.AuthContext
	if proto.Unmarshal(raw, &pb) != nil {
		return AuthContext{}, ErrAuthContextInvalid
	}
	a := AuthContext{BackendID: pb.BackendId, Account: pb.Account, SessionID: pb.SessionId, ValidUntil: time.Unix(pb.ValidUntilUnix, 0)}
	if err := a.Validate(time.Now()); err != nil {
		return AuthContext{}, err
	}
	return a, nil
}

func validAuthValue(value string, limit int, empty bool) bool {
	return (empty || value != "") && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}
