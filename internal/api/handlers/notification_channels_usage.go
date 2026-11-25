package handlers

import (
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// channelInUseResponse is the 409 body of a refused channel delete.
type channelInUseResponse struct {
	Error  string                    `json:"error"`
	UsedBy []domain.ChannelPolicyRef `json:"used_by"`
}

// withUsedBy sets UsedBy to refs, never null, so clients can count it.
func (c channelResponse) withUsedBy(refs []domain.ChannelPolicyRef) channelResponse {
	if refs == nil {
		refs = []domain.ChannelPolicyRef{}
	}
	c.UsedBy = refs
	return c
}

func writeChannelInUse(w http.ResponseWriter, refs []domain.ChannelPolicyRef) {
	if refs == nil {
		refs = []domain.ChannelPolicyRef{}
	}
	jsonResponse(w, http.StatusConflict, channelInUseResponse{
		Error:  "This channel is used by escalation policies. Remove it from each policy before deleting it.",
		UsedBy: refs,
	})
}
