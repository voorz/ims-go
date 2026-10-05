package swu

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
)

const DefaultSessionSlot = ""

type managedSession struct {
	session *Session
	cancel  context.CancelFunc
}

// SessionManager owns sessions and cancellation functions by device ID and
// optional slot. The empty slot is the default IKE session for a device.
type SessionManager struct {
	mu      sync.Mutex
	devices map[string]map[string]*managedSession
}

func NewSessionManager() *SessionManager {
	return &SessionManager{devices: make(map[string]map[string]*managedSession)}
}

// Start restores the original context/config session constructor.
func (m *SessionManager) Start(
	ctx context.Context,
	deviceID string,
	config *Config,
) (*Session, error) {
	return m.StartSlot(ctx, deviceID, DefaultSessionSlot, config)
}

// StartSlot starts another SWu session for the same device. Overlapping IKE
// reauthentication and a second PDN both use a non-default slot so the old
// default session can keep forwarding.
func (m *SessionManager) StartSlot(
	ctx context.Context,
	deviceID string,
	slot string,
	config *Config,
) (*Session, error) {
	if deviceID == "" {
		return nil, errors.New("session id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	slot = normalizeSessionSlot(slot)
	session := NewSession(config, slog.Default().With("device", deviceID, "slot", slot))
	sessionContext, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	slots := m.devices[deviceID]
	if slots == nil {
		slots = make(map[string]*managedSession)
		m.devices[deviceID] = slots
	}
	if _, exists := slots[slot]; exists {
		m.mu.Unlock()
		cancel()
		return nil, errors.New("session id 已存在")
	}
	slots[slot] = &managedSession{session: session, cancel: cancel}
	m.mu.Unlock()
	go m.run(sessionContext, session)
	return session, nil
}

func (m *SessionManager) run(ctx context.Context, session *Session) {
	if err := session.Connect(ctx); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			session.Logger.Error("SWu managed session exited", "error", err)
		}
		return
	}
	select {
	case <-ctx.Done():
		session.Shutdown()
	case <-session.done:
	}
}

// Stop removes and closes every session for the device.
func (m *SessionManager) Stop(deviceID string) error {
	m.mu.Lock()
	slots, exists := m.devices[deviceID]
	if exists {
		delete(m.devices, deviceID)
	}
	m.mu.Unlock()
	if !exists || len(slots) == 0 {
		return errors.New("session id 不存在")
	}
	stopManagedSessions(slots)
	return nil
}

// StopSlot removes one named session. Other sessions for the device stay up.
func (m *SessionManager) StopSlot(deviceID, slot string) error {
	slot = normalizeSessionSlot(slot)
	m.mu.Lock()
	slots := m.devices[deviceID]
	entry, exists := slots[slot]
	if exists {
		delete(slots, slot)
		if len(slots) == 0 {
			delete(m.devices, deviceID)
		}
	}
	m.mu.Unlock()
	if !exists {
		return errors.New("session id 不存在")
	}
	stopManagedSession(entry)
	return nil
}

// SwapDefault promotes slot to the default session and returns the retired
// default without stopping the promoted session. The caller deletes the
// retired IKE SA after the successor is forwarding.
func (m *SessionManager) SwapDefault(deviceID, slot string) (*Session, error) {
	slot = normalizeSessionSlot(slot)
	if slot == DefaultSessionSlot {
		session, ok := m.Get(deviceID)
		if !ok {
			return nil, errors.New("session id 不存在")
		}
		return session, nil
	}
	m.mu.Lock()
	slots := m.devices[deviceID]
	next, exists := slots[slot]
	if !exists || next == nil {
		m.mu.Unlock()
		return nil, errors.New("session id 不存在")
	}
	retired := slots[DefaultSessionSlot]
	delete(slots, slot)
	slots[DefaultSessionSlot] = next
	m.mu.Unlock()
	if retired == nil {
		return nil, nil
	}
	return retired.session, nil
}

// Get restores the original session-plus-presence result for the default slot.
func (m *SessionManager) Get(deviceID string) (*Session, bool) {
	return m.GetSlot(deviceID, DefaultSessionSlot)
}

// GetSlot returns a named session.
func (m *SessionManager) GetSlot(deviceID, slot string) (*Session, bool) {
	slot = normalizeSessionSlot(slot)
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, exists := m.devices[deviceID][slot]
	if !exists || entry == nil {
		return nil, false
	}
	return entry.session, true
}

// Sessions returns every live session for the device, default slot first.
func (m *SessionManager) Sessions(deviceID string) []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	slots := m.devices[deviceID]
	if len(slots) == 0 {
		return nil
	}
	out := make([]*Session, 0, len(slots))
	if entry := slots[DefaultSessionSlot]; entry != nil && entry.session != nil {
		out = append(out, entry.session)
	}
	for slot, entry := range slots {
		if slot == DefaultSessionSlot || entry == nil || entry.session == nil {
			continue
		}
		out = append(out, entry.session)
	}
	return out
}

// Register retains the additive direct-session registry behavior.
func (m *SessionManager) Register(deviceID string, session *Session) {
	m.mu.Lock()
	slots := m.devices[deviceID]
	if slots == nil {
		slots = make(map[string]*managedSession)
		m.devices[deviceID] = slots
	}
	slots[DefaultSessionSlot] = &managedSession{session: session}
	m.mu.Unlock()
}

// Lookup retains the additive nil-on-missing lookup behavior.
func (m *SessionManager) Lookup(deviceID string) *Session {
	session, _ := m.Get(deviceID)
	return session
}

// Unregister retains the additive remove-and-shutdown behavior.
func (m *SessionManager) Unregister(deviceID string) {
	m.mu.Lock()
	slots, exists := m.devices[deviceID]
	if exists {
		delete(m.devices, deviceID)
	}
	m.mu.Unlock()
	if exists {
		stopManagedSessions(slots)
	}
}

func normalizeSessionSlot(slot string) string {
	return strings.TrimSpace(slot)
}

func stopManagedSessions(slots map[string]*managedSession) {
	for _, entry := range slots {
		stopManagedSession(entry)
	}
}

func stopManagedSession(entry *managedSession) {
	if entry == nil {
		return
	}
	if entry.cancel != nil {
		entry.cancel()
	}
	if entry.session != nil {
		entry.session.Shutdown()
	}
}
