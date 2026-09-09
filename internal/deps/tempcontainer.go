package deps

// TempContainerOpts is everything about the temp container a migration runs
// that is not in the generated app def.
//
// It lives HERE rather than in internal/deps/migrate, whose Env seam it
// belongs to, for one mechanical reason: migratetest.FakeEnv has to name this
// type to implement migrate.Env, and it cannot import migrate — migrate's own
// tests are in-package and import migratetest, so the reverse edge is an
// import cycle in the test binary. migrate re-exports it as an ALIAS
// (migrate.TempContainerOpts), so every plan and the daemon still spell it in
// the package whose seam it is, and the two names are one type.
//
// Name and ExtraBinds are what the seam has always carried. Env and HostAlias
// exist for one measured reason: RabbitMQ derives its node name from the
// container hostname and its data path contains that node name, so a temp
// container running under an override name boots a fresh EMPTY node inside the
// data volume and reports healthy. Pinning the node identity needs BOTH the
// node name in the environment and a container-local /etc/hosts entry for its
// host part — the environment alone fails the boot with
// "epmd error for host rabbitmq: nxdomain".
//
// HostAlias is deliberately not a hostname override: moby registers a
// container's hostname as a DNS name on a user-defined network, so overriding
// it would make a temp container answer to the app's own name on the namespace
// network. /etc/hosts is container-local and invisible to that DNS.
type TempContainerOpts struct {
	// Name is the container name, always different from the app's own.
	Name string
	// ExtraBinds are appended to the def's volumes, "<host dir>:<container path>".
	ExtraBinds []string
	// Env is added to the def's environment on a COPY of the def; an
	// implementation must never mutate the caller's.
	Env map[string]string
	// HostAlias maps a hostname to an IP, written into the container's own
	// /etc/hosts.
	HostAlias map[string]string
}
