package client

import (
	"net/http"

	"github.com/citeck/citeck-launcher/internal/api"
)

// GetExperimentalFeatures reads the experimental (BETA) features the ACTIVE
// namespace's bundle offers, each with whether the namespace has it on —
// GET /experimental-features with no bundle in the query, which is how the
// daemon knows to answer about the active namespace and fill Enabled.
func (c *DaemonClient) GetExperimentalFeatures() (*api.ExperimentalFeaturesDto, error) {
	var dto api.ExperimentalFeaturesDto
	if err := c.get(api.ExperimentalFeatures, &dto); err != nil {
		return nil, err
	}
	return &dto, nil
}

// PutNamespaceEdit applies the typed edit form to a namespace. The DTO has
// patch semantics on the daemon side — an empty string, a zero port or a nil
// pointer leaves that field unchanged — so a caller changing one thing sends
// exactly that one field.
func (c *DaemonClient) PutNamespaceEdit(nsID string, edit api.NamespaceEditDto) (*api.ActionResultDto, error) {
	resp, err := c.doRequest(http.MethodPut, api.NamespaceEditPath(nsID), edit)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var dto api.ActionResultDto
	if err := decodeResponse(resp, &dto); err != nil {
		return nil, err
	}
	return &dto, nil
}
