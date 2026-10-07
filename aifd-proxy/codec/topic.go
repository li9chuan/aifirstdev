package codec

import "fmt"

const clientTopicPrefix = "Mc"

// ValidateClientTopic 校验客户端上行协议名。
func ValidateClientTopic(topic string) error {
	if IsClientTopic(topic) {
		return nil
	}
	return fmt.Errorf("%w: invalid client topic %q", ErrInvalidPacket, topic)
}

// IsClientTopic 判断 topic 是否符合客户端上行协议名规则。
func IsClientTopic(topic string) bool {
	if len(topic) <= len(clientTopicPrefix) {
		return false
	}
	if topic[0:len(clientTopicPrefix)] != clientTopicPrefix {
		return false
	}
	for i := 0; i < len(topic); i++ {
		ch := topic[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
			continue
		}
		return false
	}
	return true
}
