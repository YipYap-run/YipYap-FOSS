package handlers

import (
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// checkOrgMFAPlanGate is a no-op in the FOSS build, which has no plans.
func checkOrgMFAPlanGate(_ http.ResponseWriter, _ *domain.Org) bool {
	return true
}
