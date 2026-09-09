package deps

import "github.com/citeck/citeck-launcher/internal/appdef"

// keycloakDescriptor describes Keycloak, which migrates its own schema forward
// on start but cannot go back, so a major bump is held. No migrator in v1.
//
// It has no data volume: its state lives in the namespace's PostgreSQL
// database, which is why VolumeBase is "" and the pin-seeding probe reads the
// PostgreSQL data to decide whether Keycloak has ever run.
type keycloakDescriptor struct{}

func (keycloakDescriptor) ID() ID          { return Keycloak }
func (keycloakDescriptor) AppName() string { return appdef.AppKeycloak }
func (keycloakDescriptor) ParseVersion(image string) (Version, bool) {
	return ParseImageVersion(image)
}
func (keycloakDescriptor) VolumeBase() string               { return "" }
func (keycloakDescriptor) IsBreaking(from, to Version) bool { return majorBreaking(from, to) }
func (keycloakDescriptor) Migratable() bool                 { return false }

// LegacyImage is "26.4" and NOT the bare major "26": Keycloak publishes no
// bare-major tag at all — `keycloak/keycloak:26` is a 404 on Docker Hub
// (probed 2026-09-09) — so the invented pin named an image that could never be
// pulled. It only reaches a container when Keycloak is HELD BACK, i.e. when a
// bundle offers a different MAJOR, which is why it had not bitten yet; by then
// it would have been a stand whose Keycloak cannot start.
//
// 26.4 exists, floats over the PATCH only (which majorBreaking calls harmless)
// and is the minor of keycloak/keycloak:26.4.5 — the value both the Kotlin
// 1.3.9 launcher and every Go 2.x release have emitted, so it is also the
// truthful answer to "what has this namespace been running".
//
// Existing state files keep the old string: seeding runs only where there is
// no pin, so the fix is forward-only, and a table of known-bad invented pins is
// a new kind of thing to maintain for a state that fails with an actionable
// pull error (orchestrator ruling on OPEN QUESTION 9).
func (keycloakDescriptor) LegacyImage() string { return "keycloak/keycloak:26.4" }
func (keycloakDescriptor) UpgradeSupport(from, to Version) VendorSupport {
	return forwardOnlySupport(from, to)
}

// mongoDescriptor describes MongoDB: only generation-1 namespaces still run it
// (see Config.MongoEnabled).
type mongoDescriptor struct{}

func (mongoDescriptor) ID() ID          { return MongoDB }
func (mongoDescriptor) AppName() string { return appdef.AppMongodb }
func (mongoDescriptor) ParseVersion(image string) (Version, bool) {
	return ParseImageVersion(image)
}

// VolumeBase is "mongo", not the id "mongodb": the generator has always
// emitted "mongo2:/data/db" and generation 1 must spell itself exactly the way
// the volume on disk is spelled.
func (mongoDescriptor) VolumeBase() string               { return "mongo" }
func (mongoDescriptor) IsBreaking(from, to Version) bool { return majorBreaking(from, to) }
func (mongoDescriptor) Migratable() bool                 { return false }
func (mongoDescriptor) LegacyImage() string              { return "mongo:4.0" }
func (mongoDescriptor) UpgradeSupport(from, to Version) VendorSupport {
	return forwardOnlySupport(from, to)
}
