package ipsec

import "log/slog"

// logDebug/logInfo/logWarn 是 ipsec 包内的日志入口（D-008：直接使用标准库 slog）。
func logDebug(message string, args ...any) { slog.Debug(message, args...) }
func logInfo(message string, args ...any)  { slog.Info(message, args...) }
func logWarn(message string, args ...any)  { slog.Warn(message, args...) }
