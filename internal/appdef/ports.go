package appdef

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// PortSpec is one parsed `ports:` entry of an ApplicationDef:
//
//	[hostIP:]hostPort:containerPort[/protocol]
//
// "5432:5432", "127.0.0.1:15432:5432", "[::1]:15432:5432", "*:443:443",
// "17014:17014/udp".
// The HOST port may be a range ("8000-8005:80"); the container port may not.
// The host IP is what makes a published
// port reachable from this machine only — on a server, where the alternative
// is a database published on every interface.
type PortSpec struct {
	// HostIP is the address the entry names, taken literally as Docker takes
	// it: "0.0.0.0" is every IPv4 interface and nothing else, "[::]" every
	// IPv6 one. The zero Addr means the entry names no address, which the
	// container assembly publishes on 127.0.0.1
	// (docker.Client.buildCreateOptions) — unless AllInterfaces is set.
	HostIP netip.Addr
	// AllInterfaces is the "*" address: every interface of every family the
	// host has — Docker's own default for a port with no address, which binds
	// 0.0.0.0 and [::] where IPv6 exists and does not fail where it does not
	// (an explicit "[::]" would). It is how a port that must be reachable from
	// other machines says so; the proxy's generator writes it.
	AllInterfaces bool
	// HostPort and ContainerPort are as written, without the protocol.
	HostPort      string
	ContainerPort string
	// Protocol is "tcp", "udp" or "sctp" — "tcp" when the entry names none.
	Protocol string
}

// ErrPortNotPublished is ParsePortSpec's answer for a bare container port
// ("8025"): an entry with nothing to publish it on. It is its own error so the
// container assembly can keep ignoring such an entry, as it always has, while
// an edit refuses it — the operator who wrote it expected a published port.
var ErrPortNotPublished = errors.New("a bare container port publishes nothing; write hostPort:containerPort")

// ParsePortSpec reads one `ports:` entry. It is the ONE parser of that syntax:
// the container assembly, the host-port conflict check and the edit validation
// all read an entry through it, so an entry the editor accepts is an entry the
// container can be created with. (Before it, the assembly split on the first
// ':' only, and "127.0.0.1:15432:5432" failed at container START as the
// container port "15432:5432" — the app down, the reason only in the log.)
func ParsePortSpec(spec string) (PortSpec, error) {
	s := strings.TrimSpace(spec)
	if s == "" {
		return PortSpec{}, errors.New("empty port")
	}
	out := PortSpec{Protocol: "tcp"}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		out.Protocol = strings.ToLower(s[i+1:])
		s = s[:i]
		switch out.Protocol {
		case "tcp", "udp", "sctp":
		default:
			return PortSpec{}, fmt.Errorf("port %q: unknown protocol %q (tcp, udp or sctp)", spec, out.Protocol)
		}
	}

	var parts []string
	bracketed := strings.HasPrefix(s, "[")
	if bracketed {
		// An IPv6 host address is bracketed, as in a URL: "[::1]:15432:5432".
		end := strings.Index(s, "]:")
		if end < 0 {
			return PortSpec{}, fmt.Errorf("port %q: unterminated IPv6 address", spec)
		}
		parts = append([]string{s[1:end]}, strings.Split(s[end+2:], ":")...)
		if len(parts) != 3 {
			return PortSpec{}, fmt.Errorf("port %q: expected [hostIP]:hostPort:containerPort", spec)
		}
	} else {
		parts = strings.Split(s, ":")
	}
	switch len(parts) {
	case 1:
		return PortSpec{}, fmt.Errorf("port %q: %w", spec, ErrPortNotPublished)
	case 2:
		out.HostPort, out.ContainerPort = parts[0], parts[1]
	case 3:
		if parts[0] == "*" && !bracketed {
			out.AllInterfaces = true
		} else {
			ip, err := netip.ParseAddr(parts[0])
			if err != nil {
				return PortSpec{}, fmt.Errorf("port %q: host address %q is not an IP address (or \"*\" for every interface)", spec, parts[0])
			}
			out.HostIP = ip
		}
		out.HostPort, out.ContainerPort = parts[1], parts[2]
	default:
		return PortSpec{}, fmt.Errorf("port %q: expected [hostIP:]hostPort:containerPort", spec)
	}
	// An EMPTY host port (":5432", "127.0.0.1::5432") is Docker's own "pick a
	// free one" and is passed through as it always was.
	if out.HostPort != "" {
		if err := checkPortOrRange(out.HostPort); err != nil {
			return PortSpec{}, fmt.Errorf("port %q: host port: %w", spec, err)
		}
	}
	// The container side is ONE port: Docker's API keys a binding by a single
	// container port (network.ParsePort refuses "8000-8005"), so a container
	// range accepted here would fail at container create — the very class of
	// failure this parser exists to move to edit time. A host range mapping
	// to one container port is Docker's own and stays.
	if _, err := portNumber(out.ContainerPort); err != nil {
		return PortSpec{}, fmt.Errorf("port %q: container port: %w", spec, err)
	}
	return out, nil
}

// ContainerPortProto is the container side as Docker keys it: "5432/tcp".
func (p PortSpec) ContainerPortProto() string {
	return p.ContainerPort + "/" + p.Protocol
}

// SingleHostPort is the host port as a number, 0 for a range or for an empty
// (Docker-assigned) one.
func (p PortSpec) SingleHostPort() int {
	n, err := strconv.Atoi(p.HostPort)
	if err != nil {
		return 0
	}
	return n
}

func checkPortOrRange(s string) error {
	lo, hi, isRange := strings.Cut(s, "-")
	a, err := portNumber(lo)
	if err != nil {
		return err
	}
	if !isRange {
		return nil
	}
	b, err := portNumber(hi)
	if err != nil {
		return err
	}
	if b < a {
		return fmt.Errorf("range %q runs backwards", s)
	}
	return nil
}

// portNumber reads a port the way Docker does — unsigned decimal, no sign
// ("+80" would pass strconv.Atoi and then fail at the engine).
func portNumber(s string) (int, error) {
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%q is not a port number (1-65535)", s)
	}
	return int(n), nil
}
