package setup

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

type s3Setting struct{}

func (s *s3Setting) ID() string             { return "s3" }
func (s *s3Setting) Title() string          { return i18n.T("setup.s3.title") }
func (s *s3Setting) Description() string    { return i18n.T("setup.s3.desc") }
func (s *s3Setting) TargetFile() TargetFile { return NamespaceFile }

func (s *s3Setting) Available(_ *namespace.Config, apps []string) bool {
	return slices.Contains(apps, appdef.AppContent)
}

func (s *s3Setting) CurrentValue(cfg *namespace.Config, _ *config.DaemonConfig) string {
	if cfg.S3 == nil {
		return i18n.T("setup.value.not_configured")
	}
	host := cfg.S3.Endpoint
	if u, err := url.Parse(host); err == nil && u.Host != "" {
		host = u.Host
	}
	return host + " / " + cfg.S3.Bucket
}

func (s *s3Setting) Run(ctx *setupContext, cfg *namespace.Config, _ *config.DaemonConfig) error {
	// If already configured, offer edit/remove.
	if cfg.S3 != nil {
		action, err := (&prompt.Select[string]{
			Title: i18n.T("setup.s3.action"),
			Options: []prompt.Option[string]{
				{Label: i18n.T("setup.s3.edit"), Value: "edit"},
				{Label: i18n.T("setup.s3.remove"), Value: "remove"},
				{Label: i18n.T("setup.back"), Value: backValue},
			},
			Hints: hints(),
		}).Run()
		if err != nil {
			return fmt.Errorf("s3 action selection: %w", err)
		}
		if action == backValue {
			return prompt.ErrCanceled
		}
		if action == "remove" {
			cfg.S3 = nil
			return nil
		}
	}

	s3 := &namespace.S3Config{}
	if cfg.S3 != nil {
		s3 = cfg.S3
	}

	// On edit, secret key is optional — keep existing secret reference if left empty.
	secretKeyValidate := notEmpty
	if cfg.S3 != nil && cfg.S3.SecretKey != "" {
		secretKeyValidate = func(string) error { return nil }
	}

	endpoint, err := (&prompt.Input{
		Title: i18n.T("setup.s3.endpoint"),
		Value: s3.Endpoint,
		Validate: func(val string) error {
			val = strings.TrimSpace(val)
			if val == "" {
				return errors.New(i18n.T("validate.required"))
			}
			u, err := url.Parse(val)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return errors.New(i18n.T("setup.s3.invalidUrl"))
			}
			return nil
		},
		Hints: hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("s3 form: %w", err)
	}
	bucket, err := (&prompt.Input{
		Title:    i18n.T("setup.s3.bucket"),
		Value:    s3.Bucket,
		Validate: notEmpty,
		Hints:    hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("s3 form: %w", err)
	}
	accessKey, err := (&prompt.Input{
		Title:    i18n.T("setup.s3.access_key"),
		Value:    s3.AccessKey,
		Validate: notEmpty,
		Hints:    hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("s3 form: %w", err)
	}
	secretKey, err := (&prompt.Input{
		Title:    i18n.T("setup.s3.secret_key"),
		Password: true,
		Validate: secretKeyValidate,
		Hints:    hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("s3 form: %w", err)
	}
	region, err := (&prompt.Input{
		Title:       i18n.T("setup.s3.region"),
		Description: i18n.T("setup.s3.region_hint"),
		Value:       s3.Region,
		Hints:       hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("s3 form: %w", err)
	}

	applyS3Setting(ctx, cfg, s3, endpoint, bucket, accessKey, secretKey, region)
	return nil
}

// applyS3Setting writes the parsed form values into cfg and ctx.PendingSecrets.
// Plain secret values are never written to cfg — only "secret:s3.secretKey" refs,
// which are resolved at container-start time by the generator (applyS3Config).
// Extracted from Run() so the behavior can be unit tested without driving the TUI.
func applyS3Setting(ctx *setupContext, cfg *namespace.Config, prev *namespace.S3Config,
	endpoint, bucket, accessKey, secretKey, region string,
) {
	cfg.S3 = &namespace.S3Config{
		Endpoint:  strings.TrimSpace(endpoint),
		Bucket:    strings.TrimSpace(bucket),
		AccessKey: strings.TrimSpace(accessKey),
		Region:    strings.TrimSpace(region),
	}

	if secretKey != "" {
		cfg.S3.SecretKey = "secret:s3.secretKey"
		ctx.PendingSecrets["s3.secretKey"] = secretKey
	} else if prev != nil && prev.SecretKey != "" {
		cfg.S3.SecretKey = prev.SecretKey
	}
}
