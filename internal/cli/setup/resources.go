package setup

import (
	"fmt"
	"strconv"

	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
)

type resourcesSetting struct{}

func (s *resourcesSetting) ID() string             { return "resources" }
func (s *resourcesSetting) Title() string          { return i18n.T("setup.resources.title") }
func (s *resourcesSetting) Description() string    { return i18n.T("setup.resources.desc") }
func (s *resourcesSetting) TargetFile() TargetFile { return NamespaceFile }

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
		options := make([]prompt.Option[string], 0, len(apps)+1)
		for _, a := range apps {
			label := a
			if wp, ok := cfg.Webapps[a]; ok && (wp.HeapSize != "" || wp.MemoryLimit != "") {
				label = fmt.Sprintf("%s (heap=%s, mem=%s)", a, wp.HeapSize, wp.MemoryLimit)
			}
			options = append(options, prompt.Option[string]{Label: label, Value: a})
		}
		options = append(options, prompt.Option[string]{Label: i18n.T("setup.resources.done"), Value: ""})

		appName, err := (&prompt.Select[string]{
			Title:   i18n.T("setup.resources.select_app"),
			Options: options,
			Height:  prompt.DefaultSelectHeight,
			Hints:   hints(),
		}).Run()
		if err != nil {
			return fmt.Errorf("app selection: %w", err)
		}
		if appName == "" {
			return nil
		}

		wp := cfg.Webapps[appName]
		heap, herr := (&prompt.Input{
			Title:       i18n.T("setup.resources.heap", "app", appName),
			Placeholder: wp.HeapSize,
			Validate:    validateHeapFormat,
			Hints:       hints(),
		}).Run()
		if herr != nil {
			return fmt.Errorf("resources form for %s: %w", appName, herr)
		}
		mem, merr := (&prompt.Input{
			Title:       i18n.T("setup.resources.memory", "app", appName),
			Placeholder: wp.MemoryLimit,
			Hints:       hints(),
		}).Run()
		if merr != nil {
			return fmt.Errorf("resources form for %s: %w", appName, merr)
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
// persisting). Returns a non-nil error only on unexpected I/O from the
// confirm prompt.
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
		nerr := (&prompt.Note{
			Title:       i18n.T("setup.validation_error"),
			Description: guard.Err,
			Hints:       hints(),
		}).Run()
		if nerr != nil {
			return fmt.Errorf("resources guard note for %s: %w", appName, nerr)
		}
		return nil
	}
	if guard.Warning != "" {
		proceed, cerr := (&prompt.Confirm{
			Title:       guard.Warning,
			Affirmative: output.ConfirmYes,
			Negative:    output.ConfirmNo,
			Hints:       hints(),
		}).Run()
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
