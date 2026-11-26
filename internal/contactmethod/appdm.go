package contactmethod

import (
	"context"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// The self-hosted edition has no platform chat apps, so a DM contact
// method always sends through an org channel's own bot.
func checkAppInstall(context.Context, store.Store, string, domain.ContactMethodType, string) error {
	return invalid("app installs are not available on this server; choose an integration")
}

func appDMTarget(t domain.ContactMethodType, _, _ string) (string, any, error) {
	return "", nil, fmt.Errorf("%s app delivery is not available on this server", t)
}
