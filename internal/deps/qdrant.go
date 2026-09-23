package deps

// qdrantDescriptor describes Qdrant, the vector store the RAG service indexes
// into and searches.
//
// Minor bumps are held back (IsBreaking), like RabbitMQ and ZooKeeper and
// unlike PostgreSQL: Qdrant's storage compatibility is published per MINOR —
// data written by 1.16.x is read by 1.17.x and by nothing further — so a minor
// is exactly where its format break sits.
// It is INSTANTIATED PER STORE for the same reason postgresDescriptor is: a
// namespace may run more than one vector store (the built-in one and any a
// workspace declares), while the rules below — what breaks, how far the vendor
// reaches, what the legacy image is — are the same for every instance.
type qdrantDescriptor struct {
	// id is the dependency this descriptor speaks for; appName is its
	// container; volumeBase is the stem the generation counter suffixes. The
	// zero value is deliberately NOT the built-in store: a descriptor built
	// without them would pin and migrate the wrong data.
	id         ID
	appName    string
	volumeBase string
}

// IsQdrantFamily reports whether d is a Qdrant store, whichever one — the
// built-in or one a workspace declares. The counterpart of IsPostgresFamily.
func IsQdrantFamily(d Descriptor) bool {
	_, ok := d.(qdrantDescriptor)
	return ok
}

func (d qdrantDescriptor) ID() ID          { return d.id }
func (d qdrantDescriptor) AppName() string { return d.appName }
func (qdrantDescriptor) ParseVersion(image string) (Version, bool) {
	return ParseImageVersion(image)
}

// VolumeBase is "qdrant", so generation 1 is the volume "qdrant2" — the same
// spelling every other dependency's first generation has. It is deliberately
// NOT the string the generator emitted before qdrant was registered
// ("qdrant_storage"): a volume outside the generation counter cannot be
// migrated at all, because the whole mechanism of a copy upgrade is building
// the next generation beside the current one.
func (d qdrantDescriptor) VolumeBase() string             { return d.volumeBase }
func (qdrantDescriptor) IsBreaking(from, to Version) bool { return minorBreaking(from, to) }
func (qdrantDescriptor) Migratable() bool                 { return true }

// LegacyImage names a CONCRETE patch, unlike postgres/rabbitmq/zookeeper,
// because Qdrant publishes no floating tag to name: "qdrant/qdrant:v1.14"
// is a 404 on Docker Hub (probed 2026-09-15) exactly as the bare Keycloak
// major is. v1.14.1 is also the only version RAG has ever shipped with — every
// bundle that carries EcosRagApp names it — so for data whose version could
// not be read it is not a guess but the single possibility.
func (qdrantDescriptor) LegacyImage() string { return "qdrant/qdrant:v1.14.1" }

// qdrantMinorSpan is how far Qdrant's own storage compatibility reaches: ONE
// minor. The vendor states that data written by version 1.x is readable by
// 1.(x+1) and that skipping a minor is not supported, so a longer hop has to
// be walked one minor at a time.
const qdrantMinorSpan = 1

// UpgradeSupport answers Qdrant's one-minor rule.
//
// There is no hop TABLE because there is no table to copy: the rule is
// arithmetic the vendor states once and applies to every series. Across a
// MAJOR the vendor documents nothing at all, which is not the same as "go
// through the last minor of the old major first" — naming a hop there would
// invent a path, so that case answers with no path rather than with a Via.
func (qdrantDescriptor) UpgradeSupport(from, to Version) VendorSupport {
	if compareVersions(to, from) < 0 {
		return VendorSupport{} // no vendor here supports moving data backwards
	}
	if from.Major != to.Major {
		return VendorSupport{}
	}
	if to.Minor-from.Minor <= qdrantMinorSpan {
		return VendorSupport{Allowed: true}
	}
	// The NEXT hop, not the last one: the operator is told the one move they
	// can make now, and asking again after it names the one after that.
	return VendorSupport{Via: seriesString(Version{Major: from.Major, Minor: from.Minor + qdrantMinorSpan})}
}
