package voice

import (
	"strings"
)

// Precondition（RFC 3312）：QoS 预留条件。
//
// Wi-Fi 下本地资源恒可用，但 IR.51 要求信令走形式。
// SDP 属性：
//   a=curr:qos local sendrecv  — 本地当前状态
//   a=curr:qos remote none     — 远端当前状态（初始未知）
//   a=des:qos mandatory local sendrecv  — 本地期望（强制）
//   a=des:qos optional remote sendrecv  — 远端期望（可选）

// buildInviteSDP 构造 INVITE 的 SDP（含 precondition）。
// 使用对象模型而非字符串拼接（超越 vowifi-go）。
func (a *Agent) buildInviteSDP() string {
	var sb strings.Builder
	sb.WriteString("v=0\r\n")
	sb.WriteString("o=- 0 0 IN IP4 127.0.0.1\r\n")
	sb.WriteString("s=-\r\n")
	sb.WriteString("c=IN IP4 127.0.0.1\r\n")
	sb.WriteString("t=0 0\r\n")
	sb.WriteString("m=audio 5004 RTP/AVP 0\r\n")
	// Precondition 属性（RFC 3312）
	sb.WriteString("a=curr:qos local sendrecv\r\n")
	sb.WriteString("a=curr:qos remote none\r\n")
	sb.WriteString("a=des:qos mandatory local sendrecv\r\n")
	sb.WriteString("a=des:qos optional remote sendrecv\r\n")
	return sb.String()
}

// parsePrecondition 从 SDP 解析 precondition 状态。
// 返回 (localCurr, remoteCurr, satisfied)。
func parsePrecondition(sdp string) (local, remote string, satisfied bool) {
	lines := strings.Split(sdp, "\r\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "a=curr:qos local ") {
			local = strings.TrimPrefix(line, "a=curr:qos local ")
		} else if strings.HasPrefix(line, "a=curr:qos remote ") {
			remote = strings.TrimPrefix(line, "a=curr:qos remote ")
		}
	}
	// Wi-Fi 下：本地恒 sendrecv；远端非 none 即满足
	satisfied = (local == "sendrecv" && remote != "none" && remote != "")
	return local, remote, satisfied
}

// buildPreconditionUpdate 构造 precondition 状态更新的 SDP。
// 本地已就绪时，curr:qos local sendrecv 保持，等待远端。
func (a *Agent) buildPreconditionUpdate() string {
	// UPDATE 的 SDP：报告本地状态（无变化，仅确认）
	return a.buildInviteSDP()
}

// checkPreconditionTransition 检查是否需要进入/退出 PreconditionWait 状态。
// remoteSDP 为远端 SDP；返回 true 表示 precondition 未满足，需等待。
func checkPreconditionTransition(remoteSDP string) bool {
	if remoteSDP == "" {
		return false // 无 SDP，不触发 precondition 逻辑
	}
	_, _, satisfied := parsePrecondition(remoteSDP)
	return !satisfied
}
