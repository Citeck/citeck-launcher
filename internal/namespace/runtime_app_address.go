package namespace

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
)

// ErrAppAddressUnknown means the app has a container but no address the
// daemon can reach its port on was found: the port is neither published nor
// is the container attached to a network with an IP — typically a container
// that is being recreated right now. Worth retrying.
var ErrAppAddressUnknown = errors.New("app address could not be resolved")

// AppHTTPAddress answers the host:port the DAEMON can reach one of an app's
// container ports on, by the rule the startup and liveness probes already
// follow (waitForProbe, the reconciler): the published host port on loopback
// when the port is published — desktop mode publishes every app's ports — and
// the container's IP on the namespace network with the container port
// otherwise, which is server mode, where nothing but the proxy is published.
//
// containerPort <= 0 means the app's own SERVER_PORT, the port every Citeck
// webapp serves HTTP on; an app that declares none answers an error.
//
// The gate is the CONTAINER, not the RUNNING status, like jvmTargetContainer:
// whether the app is ready to answer is the caller's question, and it asks it
// over the address. Errors wrap ErrAppUnknown, ErrAppNotRunning or
// ErrAppAddressUnknown.
func (r *Runtime) AppHTTPAddress(ctx context.Context, appName string, containerPort int) (string, error) {
	containerID, port, err := r.appHTTPTarget(appName, containerPort)
	if err != nil {
		return "", err
	}
	if published := r.docker.GetPublishedPort(ctx, containerID, port); published > 0 {
		return net.JoinHostPort("127.0.0.1", strconv.Itoa(published)), nil
	}
	if ip := r.docker.GetContainerIP(ctx, containerID); ip != "" {
		return net.JoinHostPort(ip, strconv.Itoa(port)), nil
	}
	return "", fmt.Errorf("%s: %w", appName, ErrAppAddressUnknown)
}

// appHTTPTarget reads, under the lock, the container and the port to resolve.
// The Docker calls happen outside it: an inspect can take seconds, and r.mu
// is the state machine's lock.
func (r *Runtime) appHTTPTarget(appName string, containerPort int) (containerID string, port int, err error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	app, ok := r.apps[appName]
	if !ok {
		if _, known := r.generatedDefForApp(appName); known {
			return "", 0, fmt.Errorf("%s: %w", appName, ErrAppNotRunning)
		}
		return "", 0, fmt.Errorf("%s: %w", appName, ErrAppUnknown)
	}
	if app.ContainerID == "" || app.Status == AppStatusStopped || app.Status == AppStatusStopping {
		return "", 0, fmt.Errorf("%s: %w (status %s)", appName, ErrAppNotRunning, app.Status)
	}
	port = containerPort
	if port <= 0 {
		raw, _ := app.Def.Environments.Get("SERVER_PORT")
		port, err = strconv.Atoi(raw)
		if err != nil || port <= 0 {
			return "", 0, fmt.Errorf("%s declares no SERVER_PORT (%q)", appName, raw)
		}
	}
	return app.ContainerID, port, nil
}
