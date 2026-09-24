package appdef

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePortSpecReadsEveryPublishedShape(t *testing.T) {
	cases := map[string]PortSpec{
		"5432:5432":            {HostPort: "5432", ContainerPort: "5432", Protocol: "tcp"},
		"8070:80":              {HostPort: "8070", ContainerPort: "80", Protocol: "tcp"},
		"17014:17014/udp":      {HostPort: "17014", ContainerPort: "17014", Protocol: "udp"},
		"8080:80/TCP":          {HostPort: "8080", ContainerPort: "80", Protocol: "tcp"},
		"127.0.0.1:15432:5432": {HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "15432", ContainerPort: "5432", Protocol: "tcp"},
		"0.0.0.0:53:53/udp":    {HostIP: netip.MustParseAddr("0.0.0.0"), HostPort: "53", ContainerPort: "53", Protocol: "udp"},
		"[::1]:15432:5432":     {HostIP: netip.MustParseAddr("::1"), HostPort: "15432", ContainerPort: "5432", Protocol: "tcp"},
		"8000-8005:80":         {HostPort: "8000-8005", ContainerPort: "80", Protocol: "tcp"},
		":5432":                {HostPort: "", ContainerPort: "5432", Protocol: "tcp"},
		"127.0.0.1::5432":      {HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "", ContainerPort: "5432", Protocol: "tcp"},
		"*:443:443":            {AllInterfaces: true, HostPort: "443", ContainerPort: "443", Protocol: "tcp"},
		"*:53:53/udp":          {AllInterfaces: true, HostPort: "53", ContainerPort: "53", Protocol: "udp"},
	}
	for in, want := range cases {
		got, err := ParsePortSpec(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
}

// The shape that brought the postgres of a live stand down: the address was
// read as part of the container port, and the container failed at start.
func TestParsePortSpecKeepsTheHostAddressOutOfTheContainerPort(t *testing.T) {
	got, err := ParsePortSpec("127.0.0.1:15432:5432")
	require.NoError(t, err)
	assert.Equal(t, "5432/tcp", got.ContainerPortProto())
	assert.Equal(t, 15432, got.SingleHostPort())
}

func TestParsePortSpecRefusesWhatCannotBePublished(t *testing.T) {
	for _, bad := range []string{
		"", "5432:not-a-port", "x:5432", "0:5432", "5432:70000",
		"localhost:15432:5432", "1.2.3:15432:5432", "[::1:15432:5432", "[::1]:5432",
		"a:b:c:d", "1:1/udp/tcp", "1:1/foo", "9000-8000:80",
		"8000-8005:8000-8005",       // a container range: Docker keys a binding by ONE container port
		"+80:80", "80:+80", "-1:80", // a sign: Docker reads ports unsigned
		"[*]:443:443", "**:443:443", "*:443", // "*" is a whole address, three fields
	} {
		_, err := ParsePortSpec(bad)
		assert.Error(t, err, bad)
	}
}

func TestABareContainerPortIsItsOwnError(t *testing.T) {
	_, err := ParsePortSpec("8025")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrPortNotPublished)
}

func TestParsePortSpecTrimsTheEntry(t *testing.T) {
	got, err := ParsePortSpec("  8070:80\t")
	require.NoError(t, err)
	assert.Equal(t, PortSpec{HostPort: "8070", ContainerPort: "80", Protocol: "tcp"}, got)
}

func TestSingleHostPortIsZeroForARange(t *testing.T) {
	got, err := ParsePortSpec("8000-8005:80")
	require.NoError(t, err)
	assert.Equal(t, 0, got.SingleHostPort())
}
