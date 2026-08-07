package dispatch

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EnvGlobalSendsPerHour  = "CONFENGE_GLOBAL_SENDS_PER_HOUR"
	EnvMinSendGapSeconds   = "CONFENGE_MIN_SEND_GAP_SECONDS"
	EnvSendTimezone        = "CONFENGE_SEND_TIMEZONE"
	EnvSendWindowStart     = "CONFENGE_SEND_WINDOW_START"
	EnvSendWindowEnd       = "CONFENGE_SEND_WINDOW_END"
	EnvDispatchPaused      = "CONFENGE_DISPATCH_PAUSED"
	EnvDispatchPauseReason = "CONFENGE_DISPATCH_PAUSE_REASON"
)

func LoadConfig() Config {
	cfg := DefaultConfig()
	if v := strings.TrimSpace(os.Getenv(EnvGlobalSendsPerHour)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.SendsPerHour = n
		}
	}
	if v := strings.TrimSpace(os.Getenv(EnvMinSendGapSeconds)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.MinGap = time.Duration(n) * time.Second
		}
	}
	if v := strings.TrimSpace(os.Getenv(EnvSendTimezone)); v != "" {
		cfg.Timezone = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvSendWindowStart)); v != "" {
		cfg.WindowStart = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvSendWindowEnd)); v != "" {
		cfg.WindowEnd = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvDispatchPaused)); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.EnvPaused = b
		}
	}
	cfg.EnvPauseReason = strings.TrimSpace(os.Getenv(EnvDispatchPauseReason))
	return cfg
}
