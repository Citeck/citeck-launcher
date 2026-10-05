package bundle

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// ContentStorageConfig is the workspace config's `contentStorage:` block: where
// a namespace that chose S3 content storage keeps its files, and which
// workspace-declared app provides that S3.
//
//	contentStorage:
//	  s3:
//	    app: seaweedfs                 # a workspace additionalApps entry
//	    url: http://seaweedfs:8333     # the endpoint as ecos-content sees it
//	    bucket: ecos-content
//	    accessKey: citeck
//	    secretKey: ${secret:seaweedfs-s3}
//
// The launcher knows the ECOS side of the contract (ecos-content, its records,
// the default-storage config) and nothing about the provider: which container
// serves S3 is configuration, exactly like the observer. No provider name is
// spelled in Go.
//
// Older launchers ignore the key: the Go parser is not strict and Kotlin 1.x
// reads the workspace config with FAIL_ON_UNKNOWN_PROPERTIES off.
type ContentStorageConfig struct {
	// S3 is the provider of the "S3" content storage option. Nil when the
	// block names none — or names one this launcher cannot use, see
	// UnmarshalYAML.
	S3 *ContentStorageS3Config `yaml:"-"`

	// problem is why the block was unusable, "" when it is fine. Read by
	// parseWorkspaceConfig, which has the logger and drops the block.
	problem string
}

// ContentStorageS3Config is one S3 provider. Every field is required: the
// provider is useless to ecos-content without the endpoint, the bucket and
// the credentials, so a block missing any of them is treated as absent.
type ContentStorageS3Config struct {
	// App is the provider app id — a workspace `additionalApps:` entry. A
	// namespace that chose S3 depends on it and starts it with the namespace.
	App string
	// URL is the S3 endpoint as ecos-content reaches it on the namespace
	// network (http://<app>:<port>).
	URL    string
	Bucket string
	// AccessKey / SecretKey are the S3 identity. SecretKey is meant to be a
	// `${secret:<id>}` reference to the namespace's own value — the same one
	// the provider's container is configured with.
	AccessKey string
	SecretKey string
}

// UnmarshalYAML reads the block and is deliberately TOTAL: it never reports an
// error. parseWorkspaceConfig drops the ENTIRE workspace config on a YAML error
// — imageRepos, webapps, bundleRepos and all — so a block this launcher cannot
// read must cost nothing but itself, the rule DependencyEntry follows for the
// same reason. An unusable block is remembered as a problem; the caller logs it
// once and treats the block as absent.
func (c *ContentStorageConfig) UnmarshalYAML(node *yaml.Node) error {
	*c = ContentStorageConfig{}
	var raw struct {
		S3 *struct {
			App       string `yaml:"app"`
			URL       string `yaml:"url"`
			Bucket    string `yaml:"bucket"`
			AccessKey string `yaml:"accessKey"`
			SecretKey string `yaml:"secretKey"`
		} `yaml:"s3"`
	}
	if err := node.Decode(&raw); err != nil {
		// Swallowed on purpose, not overlooked: see the doc comment.
		c.problem = "unreadable: " + err.Error()
		return nil
	}
	if raw.S3 == nil {
		return nil
	}
	s3 := &ContentStorageS3Config{
		App:       strings.TrimSpace(raw.S3.App),
		URL:       strings.TrimSpace(raw.S3.URL),
		Bucket:    strings.TrimSpace(raw.S3.Bucket),
		AccessKey: strings.TrimSpace(raw.S3.AccessKey),
		SecretKey: strings.TrimSpace(raw.S3.SecretKey),
	}
	var missing []string
	for _, f := range []struct{ name, value string }{
		{"app", s3.App}, {"url", s3.URL}, {"bucket", s3.Bucket},
		{"accessKey", s3.AccessKey}, {"secretKey", s3.SecretKey},
	} {
		if f.value == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		c.problem = "s3 is missing " + strings.Join(missing, ", ")
		return nil
	}
	c.S3 = s3
	return nil
}

// Problem is why the block could not be used, "" when it can.
func (c *ContentStorageConfig) Problem() string {
	if c == nil {
		return ""
	}
	return c.problem
}

// ContentStorageS3 answers the workspace's S3 content-storage provider, nil
// when it declares none. A nil receiver answers nil, so callers need not check
// for a missing workspace config first.
func (w *WorkspaceConfig) ContentStorageS3() *ContentStorageS3Config {
	if w == nil || w.ContentStorage == nil {
		return nil
	}
	return w.ContentStorage.S3
}

// AdditionalApp answers the ENABLED raw additionalApps entry with this name.
// Typed entries (POSTGRES, QDRANT) and disabled ones answer false: neither
// becomes a container the way a raw entry does.
func (w *WorkspaceConfig) AdditionalApp(name string) (AdditionalAppProps, bool) {
	if w == nil {
		return AdditionalAppProps{}, false
	}
	name = strings.TrimSpace(name)
	for _, a := range w.AdditionalApps {
		if strings.TrimSpace(a.Name) == name && a.IsEnabled() && !a.IsTyped() {
			return a, true
		}
	}
	return AdditionalAppProps{}, false
}
