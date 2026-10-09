package namespace

import (
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/msg"
)

// ContentStorageTransition is the pure write policy shared by online and offline writers.
// Availability must be resolved by the caller only when RequiresAvailability is true.
type ContentStorageTransition struct {
	Downgrade            bool
	RequiresAvailability bool
}

// CheckContentStorageTransition requires availability only for a new S3 selection.
func CheckContentStorageTransition(before ContentStorageType, beforeBundle bundle.Ref, next *Config) ContentStorageTransition {
	if next == nil {
		return ContentStorageTransition{}
	}
	after := next.ContentStorageType()
	return ContentStorageTransition{
		Downgrade:            before == ContentStorageS3 && after != ContentStorageS3,
		RequiresAvailability: after == ContentStorageS3 && (before != ContentStorageS3 || beforeBundle != next.BundleRef),
	}
}

// ContentStorageUnavailableMessage is the operator's sentence for one reason.
func ContentStorageUnavailableMessage(reason string, ref bundle.Ref, provider string) msg.Message {
	switch reason {
	case ContentStorageNoContentApp:
		return msg.New("contentStorage.msg.unavailable.noContentApp", "bundle", ref.String())
	case ContentStorageNoProviderConfig:
		return msg.New("contentStorage.msg.unavailable.noProviderConfig")
	case ContentStorageNoProviderApp:
		return msg.New("contentStorage.msg.unavailable.noProviderApp", "app", provider)
	case ContentStorageNoProviderImage:
		return msg.New("contentStorage.msg.unavailable.noProviderImage", "app", provider, "bundle", ref.String())
	case ContentStorageProviderConflict:
		return msg.New("contentStorage.msg.unavailable.providerConflict", "app", provider)
	default:
		return msg.New("contentStorage.msg.unavailable.bundleUnresolved", "bundle", ref.String())
	}
}

// ExternalS3Downgrade prevents removing access to content already stored outside
// the database. Changing to another S3 configuration is not a data migration.
func ExternalS3Downgrade(beforeExternal bool, next *Config) bool {
	return beforeExternal && next != nil && !next.UsesS3ContentStorage()
}
