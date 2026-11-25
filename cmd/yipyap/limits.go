package main

import (
	"log"
	"os"
	"strconv"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/config"
	"github.com/YipYap-run/YipYap-FOSS/internal/limits"
)

// setupOperatorCaps reads the operator-set resource caps. The self-hosted
// build has no plan limits, so nothing is capped unless the operator sets one
// of the YIPYAP_MAX_* variables. An invalid value stops startup.
func setupOperatorCaps() error {
	caps, err := limits.FromEnv(os.Getenv)
	if err != nil {
		return err
	}
	limits.Set(caps)
	log.Printf("operator caps per org: monitors=%s notification_channels=%s escalation_policies=%s members=%s, min check interval %ds",
		capString(caps.MaxMonitors), capString(caps.MaxNotificationChannels),
		capString(caps.MaxEscalationPolicies), capString(caps.MaxMembers), caps.MinCheckIntervalSeconds)
	return nil
}

// applyOperatorLimits installs the optional caps a self-hosted operator set.
// With none set there is no monitor or channel cap.
func applyOperatorLimits(cfg *config.Config) {
	handlers.SetOperatorLimits(handlers.OperatorLimits{
		MaxMonitorsPerOrg:             cfg.MaxMonitorsPerOrg,
		MaxNotificationChannelsPerOrg: cfg.MaxNotificationChannelsPerOrg,
		MinCheckIntervalSeconds:       cfg.MinCheckIntervalSeconds,
	})
	l := handlers.CurrentOperatorLimits()
	log.Printf("operator limits: monitors per org %s, notification channels per org %s, minimum check interval %ds",
		capString(l.MaxMonitorsPerOrg), capString(l.MaxNotificationChannelsPerOrg), l.MinCheckIntervalSeconds)
}

func capString(n int) string {
	if n <= 0 {
		return "unlimited"
	}
	return strconv.Itoa(n)
}
