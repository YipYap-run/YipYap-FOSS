package handlers

import "github.com/YipYap-run/YipYap-FOSS/internal/domain"

// The FOSS build has no platform SMS or voice channels, so no channel type
// needs phone verification.

func channelVerification(_ *domain.NotificationChannel) *channelVerificationInfo { return nil }

func channelAwaitingVerification(_ *domain.NotificationChannel) bool { return false }

func phoneTargetChanged(_, _, _ string) bool { return false }
