package auth

import (
	"fmt"

	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	"google.golang.org/protobuf/proto"
)

// CanonicalPayloadBytes returns deterministic bytes used by account signing and proxy verification.
func CanonicalPayloadBytes(payload *pbmsg.MAuthPayload) ([]byte, error) {
	if payload == nil {
		return nil, fmt.Errorf("auth payload is nil")
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(payload)
}
