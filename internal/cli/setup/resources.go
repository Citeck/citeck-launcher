package setup

import (
	"fmt"
	"strconv"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"

	"github.com/charmbracelet/huh"
	"github.com/citeck/citeck-launcher/internal/output"
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
		return i18n.T("setup.value.defaults")
	}
	return i18n.T("setup.value.apps_customized", "count", strconv.Itoa(count))
}

func (s *resourcesSetting) Run(ctx *setupContext, cfg *namespace.Config, _ *config.DaemonConfig) error {
	apps := ctx.CurrentApps
	if len(apps) == 0 {
		return fmt.Errorf("no apps available")
	}

	for {
		// Default focus to the first app so the user sees the app list
		// instead of "Done" — the zero value "" matches the Done option.
		appName := apps[0]
		options := make([]huh.Option[string], 0, len(apps)+1)
		for _, a := range apps {
			label := a
			if wp, ok := cfg.Webapps[a]; ok && (wp.HeapSize != "" || wp.MemoryLimit != "") {
				label = fmt.Sprintf("%s (heap=%s, mem=%s)", a, wp.HeapSize, wp.MemoryLimit)
			}
			options = append(options, huh.NewOption(label, a))
		}
		options = append(options, huh.NewOption(i18n.T("setup.resources.done"), ""))

		sel := huh.NewSelect[string]().
			Title(i18n.T("setup.resources.select_app")).
			Description(i18n.T("hint.select.setting")).
			Options(options...).
			Value(&appName)
		sel = output.ApplySelectHeight(sel, len(options))
		err := output.RunField(sel)
		if err != nil {
			return fmt.Errorf("app selection: %w", err)
		}
		if appName == "" {
			return nil
		}

		wp := cfg.Webapps[appName]
		var heap, mem string
		err = output.RunForm(huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title(i18n.T("setup.resources.heap", "app", appName)).
					Placeholder(wp.HeapSize).
					Value(&heap).
					Validate(validateHeapFormat),
				huh.NewInput().
					Title(i18n.T("setup.resources.memory", "app", appName)).
					Placeholder(wp.MemoryLimit).
					Value(&mem),
			),
		))
		if err != nil {
			return fmt.Errorf("resources form for %s: %w", appName, err)
		}

		if heap == "" && mem == "" {
			continue
		}
		if aerr := applyResourceInput(cfg, appName, wp, heap, mem); aerr != nil {
			return aerr
		}
	}
}

// applyResourceInput validates the entered heap/mem pair against the app's
// effective config and either persists the change or silently skips (hard
// guard failure prints an error to stdout; warning-declined skips without
// persisting). Returns a non-nil error only on unexpected I/O from huh.
func applyResourceInput(cfg *namespace.Config, appName string, wp namespace.WebappProps, heap, mem string) error {
	effHeap := heap
	if effHeap == "" {
		effHeap = wp.HeapSize
	}
	effMem := mem
	if effMem == "" {
		effMem = wp.MemoryLimit
	}
	guard := checkHeapVsMem(effHeap, effMem)
	if guard.Err != "" {
		// Use huh.NewNote so the message survives the next alt-screen repaint
		// from huh.NewSelect; output.PrintText would get wiped immediately.
		nerr := output.RunField(huh.NewNote().
			Title(i18n.T("setup.validation_error")).
			Description(guard.Err).
			Next(true))
		if nerr != nil {
			return fmt.Errorf("resources guard note for %s: %w", appName, nerr)
		}
		return nil
	}
	if guard.Warning != "" {
		var proceed bool
		cerr := output.RunField(huh.NewConfirm().
			Title(guard.Warning).
			Affirmative(output.ConfirmYes).
			Negative(output.ConfirmNo).
			Value(&proceed))
		if cerr != nil {
			return fmt.Errorf("resources guard confirm for %s: %w", appName, cerr)
		}
		if !proceed {
			return nil
		}
	}
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
	return nil
}
