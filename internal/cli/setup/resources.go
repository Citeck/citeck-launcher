package setup

import (
	"fmt"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"

	"github.com/charmbracelet/huh"
)

type resourcesSetting struct{}

func (s *resourcesSetting) ID() string             { return "resources" }
func (s *resourcesSetting) Title() string           { return i18n.T("setup.resources.title") }
func (s *resourcesSetting) Description() string     { return i18n.T("setup.resources.desc") }
func (s *resourcesSetting) TargetFile() TargetFile  { return NamespaceFile }

func (s *resourcesSetting) Available(_ *namespace.Config, apps []string) bool {
	return len(apps) > 0
}

func (s *resourcesSetting) CurrentValue(cfg *namespace.Config, _ *config.DaemonConfig) string {
	count := 0
	for _, wp := range cfg.Webapps {
		if wp.HeapSize != "" || wp.MemoryLimit != "" {
			count++
		}
	}
	if count == 0 {
		return "defaults"
	}
	if count == 1 {
		return "1 app customized"
	}
	return fmt.Sprintf("%d apps customized", count)
}

func (s *resourcesSetting) Run(ctx *setupContext, cfg *namespace.Config, _ *config.DaemonConfig) error {
	apps := ctx.CurrentApps
	if len(apps) == 0 {
		return fmt.Errorf("no apps available")
	}

	for {
		var appName string
		options := make([]huh.Option[string], 0, len(apps)+1)
		for _, a := range apps {
			label := a
			if wp, ok := cfg.Webapps[a]; ok && (wp.HeapSize != "" || wp.MemoryLimit != "") {
				label = fmt.Sprintf("%s (heap=%s, mem=%s)", a, wp.HeapSize, wp.MemoryLimit)
			}
			options = append(options, huh.NewOption(label, a))
		}
		options = append(options, huh.NewOption(i18n.T("setup.resources.done"), ""))

		err := huh.NewSelect[string]().
			Title(i18n.T("setup.resources.select_app")).
			Options(options...).
			Value(&appName).
			Run()
		if err != nil {
			return fmt.Errorf("app selection: %w", err)
		}
		if appName == "" {
			return nil
		}

		wp := cfg.Webapps[appName]
		var heap, mem string
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title(i18n.T("setup.resources.heap", "app", appName)).
					Placeholder(wp.HeapSize).
					Value(&heap),
				huh.NewInput().
					Title(i18n.T("setup.resources.memory", "app", appName)).
					Placeholder(wp.MemoryLimit).
					Value(&mem),
			),
		).Run()
		if err != nil {
			return fmt.Errorf("resources form for %s: %w", appName, err)
		}

		if heap != "" || mem != "" {
			if cfg.Webapps == nil {
				cfg.Webapps = make(map[string]namespace.WebappProps)
			}
			existing := cfg.Webapps[appName]
			if heap != "" {
				existing.HeapSize = heap
			}
			if mem != "" {
				existing.MemoryLimit = mem
			}
			cfg.Webapps[appName] = existing
		}
	}
}
