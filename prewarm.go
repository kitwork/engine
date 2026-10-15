package engine

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/kitwork/engine/core"
)

func prewarmConfiguredSites(handler *core.Engine, cfg *Config) error {
	if handler == nil || cfg == nil || !cfg.Web {
		return nil
	}

	switch cfg.Prewarm.Mode {
	case PrewarmDisabled:
		return nil
	case PrewarmAll:
		started := time.Now()
		warmed, failed := handler.Prewarm()
		if failed > 0 {
			return fmt.Errorf("app.prewarm(\"*\"): %d of %d sites failed preparation", failed, warmed+failed)
		}
		slog.Info("Prewarm ready", "sites", warmed, "elapsed", time.Since(started))
		return nil
	case PrewarmSelected:
		return prewarmSelectedSites(handler, cfg.Prewarm.Domains)
	case PrewarmPrimary:
		if cfg.Hostname == "" {
			return fmt.Errorf("app.prewarm() requires app.hostname(...) or explicit domains")
		}
		return prewarmSelectedSites(handler, []string{cfg.Hostname})
	case PrewarmDefault:
		return nil
	default:
		return fmt.Errorf("unsupported prewarm mode %q", cfg.Prewarm.Mode)
	}
}

func prewarmSelectedSites(handler *core.Engine, domains []string) error {
	started := time.Now()
	var joined error
	for _, domain := range domains {
		if err := handler.PrewarmDomain(domain); err != nil {
			joined = errors.Join(joined, fmt.Errorf("prewarm %s: %w", domain, err))
		}
	}
	if joined != nil {
		return joined
	}
	slog.Info("Prewarm ready", "sites", len(domains), "elapsed", time.Since(started))
	return nil
}
