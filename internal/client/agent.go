package client

import (
	"net/url"

	"github.com/citeck/citeck-launcher/internal/api"
)

// GetAgentCapabilities reads the running daemon's automation discovery document.
func (c *DaemonClient) GetAgentCapabilities() (*api.AgentCapabilitiesDto, error) {
	var dto api.AgentCapabilitiesDto
	if err := c.get(api.AgentCapabilities, &dto); err != nil {
		return nil, err
	}
	return &dto, nil
}

// GetAgentOperation reads the contract for a stable documented operation id.
func (c *DaemonClient) GetAgentOperation(id string) (*api.AgentOperationDto, error) {
	var dto api.AgentOperationDto
	if err := c.get(api.AgentCapabilities+"/operations/"+url.PathEscape(id), &dto); err != nil {
		return nil, err
	}
	return &dto, nil
}
