//go:build !linux

package jvmattach

// Supported reports whether dynamic attach can work on this host at all. On
// macOS and Windows the launcher's containers run inside a VM, so there is no
// /proc/<pid> for the host to reach through and no host pid to signal; callers
// fall back to signalling the JVM from inside the container.
const Supported = false

func sendQuit(_ int) error {
	return ErrUnsupported
}
