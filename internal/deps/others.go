package deps

import "github.com/citeck/citeck-launcher/internal/appdef"

// rabbitDescriptor describes RabbitMQ: the vendor supports upgrades only to the
// NEXT minor, requires all stable feature flags enabled before each, and does
// not support downgrades — so a minor bump is a migration, not a recreate.
// No migrator in v1.
type rabbitDescriptor struct{}

func (rabbitDescriptor) ID() ID          { return RabbitMQ }
func (rabbitDescriptor) AppName() string { return appdef.AppRabbitmq }
func (rabbitDescriptor) ParseVersion(image string) (Version, bool) {
	return ParseImageVersion(image)
}
func (rabbitDescriptor) IsBreaking(from, to Version) bool { return minorBreaking(from, to) }
func (rabbitDescriptor) Migratable() bool                 { return false }
func (rabbitDescriptor) LegacyImage() string              { return "rabbitmq:4.1-management" }

// zookeeperDescriptor describes ZooKeeper, which holds coordination data; minor
// bumps are rare and the safe direction is to hold them. No migrator in v1.
type zookeeperDescriptor struct{}

func (zookeeperDescriptor) ID() ID          { return Zookeeper }
func (zookeeperDescriptor) AppName() string { return appdef.AppZookeeper }
func (zookeeperDescriptor) ParseVersion(image string) (Version, bool) {
	return ParseImageVersion(image)
}
func (zookeeperDescriptor) IsBreaking(from, to Version) bool { return minorBreaking(from, to) }
func (zookeeperDescriptor) Migratable() bool                 { return false }
func (zookeeperDescriptor) LegacyImage() string              { return "zookeeper:3.9" }

// keycloakDescriptor describes Keycloak, which migrates its own schema forward
// on start but cannot go back, so a major bump is held. No migrator in v1.
type keycloakDescriptor struct{}

func (keycloakDescriptor) ID() ID          { return Keycloak }
func (keycloakDescriptor) AppName() string { return appdef.AppKeycloak }
func (keycloakDescriptor) ParseVersion(image string) (Version, bool) {
	return ParseImageVersion(image)
}
func (keycloakDescriptor) IsBreaking(from, to Version) bool { return majorBreaking(from, to) }
func (keycloakDescriptor) Migratable() bool                 { return false }
func (keycloakDescriptor) LegacyImage() string              { return "keycloak/keycloak:26" }

// mongoDescriptor describes MongoDB: only generation-1 namespaces still run it
// (see Config.MongoEnabled).
type mongoDescriptor struct{}

func (mongoDescriptor) ID() ID          { return MongoDB }
func (mongoDescriptor) AppName() string { return appdef.AppMongodb }
func (mongoDescriptor) ParseVersion(image string) (Version, bool) {
	return ParseImageVersion(image)
}
func (mongoDescriptor) IsBreaking(from, to Version) bool { return majorBreaking(from, to) }
func (mongoDescriptor) Migratable() bool                 { return false }
func (mongoDescriptor) LegacyImage() string              { return "mongo:4.0" }
