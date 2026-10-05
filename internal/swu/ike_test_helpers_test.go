package swu

import (
	"github.com/voorz/ims-go/internal/swu/ikev2"
)

// answerEmptyInformational 回复一个空 INFORMATIONAL 响应
// （原定义于已按 D-006 排除的 restoration 测试文件中，network_events_test.go 仍需要它）。
func answerEmptyInformational(session *Session, transport *testIKETransport) {
	raw := <-transport.sentIKE
	request, _ := ikev2.DecodePacket(raw)
	response := &ikev2.IKEPacket{Header: newIKEHeader(
		request.InitiatorSPI, request.ResponderSPI, request.ExchangeType, ikeResponseFlag, request.MessageID,
	)}
	encoded, _ := session.encryptAndWrap(response)
	transport.ike <- encoded
}
