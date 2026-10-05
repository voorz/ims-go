package swu

import (
	"errors"
	"log/slog"
)

var ErrCookieRequired = errors.New("需要重新发送带 COOKIE 的 IKE_SA_INIT")

func (s *Session) handleCookie(cookieData []byte) error {
	slog.Debug("收到 COOKIE，重新发送 IKE_SA_INIT", "len", len(cookieData))
	s.cookie = append([]byte(nil), cookieData...)
	s.sendCookie = true
	return nil
}
