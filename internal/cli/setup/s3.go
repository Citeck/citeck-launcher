package setup

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"

	"github.com/charmbracelet/huh"
	"github.com/citeck/citeck-launcher/internal/output"
)

type s3Setting struct{}

func (s *s3Setting) ID() string             { return "s3" }
func (s *s3Setting) Title() string           { return i18n.T("setup.s3.title") }
func (s *s3Setting) Description() string     { return i18n.T("setup.s3.desc") }
func (s *s3Setting) TargetFile() TargetFile  { return NamespaceFile }

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
		var action string
		err := huh.NewSelect[string]().
			Title(i18n.T("setup.s3.action")).
			Options(
				huh.NewOption(i18n.T("setup.s3.edit"), "edit"),
				huh.NewOption(i18n.T("setup.s3.remove"), "remove"),
			).
			Value(&action).
			WithTheme(output.HuhTheme).
		Run()
		if err != nil {
			return fmt.Errorf("s3 action selection: %w", err)
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

	var endpoint, bucket, accessKey, secretKey, region string
	endpoint = s3.Endpoint
	bucket = s3.Bucket
	accessKey = s3.AccessKey
	region = s3.Region

	// On edit, secret key is optional — keep existing secret reference if left empty.
	secretKeyValidate := notEmpty
	if cfg.S3 != nil && cfg.S3.SecretKey != "" {
		secretKeyValidate = func(string) error { return nil }
	}

	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title(i18n.T("setup.s3.endpoint")).Value(&endpoint).
				Validate(func(val string) error {
					val = strings.TrimSpace(val)
					if val == "" {
						return fmt.Errorf("endpoint is required")
					}
					u, err := url.Parse(val)
					if err != nil || u.Scheme == "" || u.Host == "" {
						return fmt.Errorf("invalid URL — must include scheme (e.g. https://s3.example.com)")
					}
					return nil
				}),
			huh.NewInput().Title(i18n.T("setup.s3.bucket")).Value(&bucket).Validate(notEmpty),
			huh.NewInput().Title(i18n.T("setup.s3.access_key")).Value(&accessKey).Validate(notEmpty),
			huh.NewInput().Title(i18n.T("setup.s3.secret_key")).Value(&secretKey).
				EchoMode(huh.EchoModePassword).Validate(secretKeyValidate),
			huh.NewInput().Title(i18n.T("setup.s3.region")).Value(&region).
				Description(i18n.T("setup.s3.region_hint")),
		),
	).WithTheme(output.HuhTheme).Run()
	if err != nil {
		return fmt.Errorf("s3 form: %w", err)
	}

	cfg.S3 = &namespace.S3Config{
		Endpoint:  strings.TrimSpace(endpoint),
		Bucket:    strings.TrimSpace(bucket),
		AccessKey: strings.TrimSpace(accessKey),
		Region:    strings.TrimSpace(region),
	}

	if secretKey != "" {
		cfg.S3.SecretKey = "secret:s3.secretKey"
		ctx.PendingSecrets["s3.secretKey"] = secretKey
	} else if s3.SecretKey != "" {
		cfg.S3.SecretKey = s3.SecretKey
	}

	return nil
}
