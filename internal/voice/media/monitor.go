package media

import (
	"time"
)

// 单通方向常量。
const (
	DirectionIMSToLAN = "IMS->LAN"
	DirectionLANToIMS = "LAN->IMS"
)

// NewRTPMonitor 创建监测器。
func NewRTPMonitor() *RTPMonitor {
	return &RTPMonitor{stopCh: make(chan struct{})}
}

// UpdateIMS 记录 IMS→LAN 方向活动。
func (m *RTPMonitor) UpdateIMS() {
	if m == nil {
		return
	}
	now := time.Now().UnixNano()
	m.lastIMStoLAN.Store(now)
	m.imsCount.Add(1)
}

// UpdateLAN 记录 LAN→IMS 方向活动。
func (m *RTPMonitor) UpdateLAN() {
	if m == nil {
		return
	}
	now := time.Now().UnixNano()
	m.lastLANtoIMS.Store(now)
	m.lanCount.Add(1)
}

// Counts 返回双向包计数。
func (m *RTPMonitor) Counts() (ims, lan uint64) {
	if m == nil {
		return 0, 0
	}
	return m.imsCount.Load(), m.lanCount.Load()
}

// CheckOneWay 检查单通：一个方向有包，另一个方向超时无包。
// 返回单通方向，空字符串表示正常。
func (m *RTPMonitor) CheckOneWay(timeout time.Duration) string {
	if m == nil || timeout <= 0 {
		return ""
	}
	now := time.Now().UnixNano()
	lastIMS := m.lastIMStoLAN.Load()
	lastLAN := m.lastLANtoIMS.Load()

	// 两个方向都没包：未开始，不算单通。
	if lastIMS == 0 && lastLAN == 0 {
		return ""
	}
	// IMS→LAN 有包，LAN→IMS 超时无包。
	if lastIMS > 0 && now-lastLAN > int64(timeout) {
		// LAN 方向从未有包，或超时。
		if lastLAN == 0 || now-lastLAN > int64(timeout) {
			return DirectionIMSToLAN
		}
	}
	// LAN→IMS 有包，IMS→LAN 超时无包。
	if lastLAN > 0 && now-lastIMS > int64(timeout) {
		if lastIMS == 0 || now-lastIMS > int64(timeout) {
			return DirectionLANToIMS
		}
	}
	return ""
}

// StartOneWayMonitor 启动单通监测循环。
func (m *RTPMonitor) StartOneWayMonitor(timeout time.Duration, onOneWay func(direction string, silentFor time.Duration)) {
	if m == nil || timeout <= 0 || onOneWay == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(timeout / 2)
		defer ticker.Stop()
		var lastReport string
		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				dir := m.CheckOneWay(timeout)
				if dir != "" && dir != lastReport {
					var silentFor time.Duration
					now := time.Now().UnixNano()
					if dir == DirectionIMSToLAN {
						silentFor = time.Duration(now - m.lastLANtoIMS.Load())
					} else {
						silentFor = time.Duration(now - m.lastIMStoLAN.Load())
					}
					onOneWay(dir, silentFor)
					lastReport = dir
				} else if dir == "" {
					lastReport = ""
				}
			}
		}
	}()
}

// Stop 停止监测。
func (m *RTPMonitor) Stop() {
	if m == nil {
		return
	}
	select {
	case <-m.stopCh:
	default:
		close(m.stopCh)
	}
}
