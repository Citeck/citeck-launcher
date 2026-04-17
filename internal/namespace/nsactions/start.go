package nsactions

import "time"

const (
	// ContainerCreateRetries matches Kotlin AppStartAction (3 retries).
	ContainerCreateRetries = 3
	// ContainerCreateRetryDelay between create attempts.
	ContainerCreateRetryDelay = 2 * time.Second
)
