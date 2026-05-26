package providers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
)

// resolveConfig decrypts and unmarshals the job's TargetConfig into dst.
// If TargetConfig is empty, returns nil (caller should use fallback config).
func resolveConfig(job domain.NotificationJob, decrypt notify.DecryptFunc, dst any) error {
	if job.TargetConfig == "" {
		return nil
	}

	// Decode base64 envelope.
	data, err := base64.StdEncoding.DecodeString(job.TargetConfig)
	if err != nil {
		// Not base64  - try raw JSON (legacy / tests).
		data = []byte(job.TargetConfig)
	} else if len(data) > 0 {
		// Check version prefix.
		version := data[0]
		payload := data[1:]
		switch version {
		case 0x01: // Encrypted
			if decrypt == nil {
				return fmt.Errorf("encrypted config but no decrypt function configured")
			}
			decrypted, err := decrypt(payload)
			if err != nil {
				return fmt.Errorf("decrypt target config: %w", err)
			}
			data = decrypted
		case 0x00: // Plaintext
			data = payload
		default:
			return fmt.Errorf("unknown config version: %d", version)
		}
	}

	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("unmarshal target config: %w", err)
	}
	return nil
}
