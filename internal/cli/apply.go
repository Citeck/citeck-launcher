package cli

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newApplyCmd() *cobra.Command {
	var (
		file    string
		wait    bool
		timeout int
		dryRun  bool
	)

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply namespace configuration (idempotent)",
		Long:  "Apply a namespace configuration file. Like kubectl apply, this is idempotent.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return fmt.Errorf("--file / -f is required")
			}

			// Parse the config file
			cfg, err := namespace.LoadNamespaceConfig(file)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if dryRun {
				result := map[string]any{
					"dryRun":  true,
					"config":  cfg,
					"message": "Configuration is valid. No changes applied.",
				}
				output.PrintResult(result, func() {
					output.PrintText("Dry run: configuration is valid")
					output.PrintText("  Name:   %s", cfg.Name)
					output.PrintText("  Bundle: %s", cfg.BundleRef.String())
					output.PrintText("  Auth:   %s", cfg.Authentication.Type)
					output.PrintText("  Proxy:  %s:%d (TLS: %v)", cfg.Proxy.Host, cfg.Proxy.Port, cfg.Proxy.TLS.Enabled)
					output.PrintText("No changes applied (dry run)")
				})
				return nil
			}

			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			// Read config file once (avoid TOCTOU)
			yamlData, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("read config file: %w", err)
			}

			// Show diff before applying
			showConfigDiffData(c, yamlData)
			if _, err := c.PutConfig(yamlData); err != nil {
				return fmt.Errorf("upload config: %w", err)
			}

			// Reload the namespace with the new config
			result, err := c.ReloadNamespace()
			if err != nil {
				return fmt.Errorf("reload namespace: %w", err)
			}

			output.PrintResult(result, func() {
				output.PrintText("Configuration applied: %s", result.Message)
			})

			if wait {
				// Subscribe to SSE stream, then check initial state.
				// This is race-safe: if RUNNING happened between reload and subscribe,
				// GetNamespace below catches it; if between subscribe and GetNamespace, the event is buffered.
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
				defer cancel()
				events, sseErr := c.StreamEvents(ctx)
				if sseErr != nil {
					return fmt.Errorf("connect to event stream: %w", sseErr)
				}

				output.Errf("Waiting for all apps to be running...")
				// Check initial state after subscribing
				if ns, err := c.GetNamespace(); err == nil && ns.Status == "RUNNING" {
					output.PrintText("All apps running")
					return nil
				}
				for {
					select {
					case <-ctx.Done():
						return fmt.Errorf("timeout waiting for namespace to be running")
					case evt, ok := <-events:
						if !ok {
							return fmt.Errorf("event stream closed")
						}
						if evt.Type == "namespace_status" && evt.After == "RUNNING" {
							output.PrintText("All apps running")
							return nil
						}
						if evt.Type == "namespace_status" && evt.After == "STALLED" {
							return fmt.Errorf("namespace stalled — some apps failed to start")
						}
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to namespace.yml")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for all apps to be running")
	cmd.Flags().IntVar(&timeout, "timeout", 600, "Wait timeout in seconds")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview changes without applying")

	return cmd
}

// showConfigDiffData fetches the current config from daemon and prints structural differences.
func showConfigDiffData(c *client.DaemonClient, newData []byte) {
	currentYAML, err := c.GetConfig()
	if err != nil {
		return // daemon may not have config loaded yet
	}

	var current, new_ map[string]any
	if yaml.Unmarshal([]byte(currentYAML), &current) != nil {
		return
	}
	if yaml.Unmarshal(newData, &new_) != nil {
		return
	}

	changes := diffMaps("", current, new_)
	if len(changes) == 0 {
		output.PrintText("No configuration changes detected.")
		return
	}

	output.PrintText("Configuration changes:")
	for _, ch := range changes {
		output.PrintText("  %s", ch)
	}
	output.PrintText("")
}

// diffMaps recursively compares two YAML maps and returns human-readable change descriptions.
func diffMaps(prefix string, old, new_ map[string]any) []string {
	var changes []string

	for key, newVal := range new_ {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		oldVal, exists := old[key]
		if !exists {
			changes = append(changes, fmt.Sprintf("+ %s: %v", path, newVal))
			continue
		}

		oldMap, oldIsMap := oldVal.(map[string]any)
		newMap, newIsMap := newVal.(map[string]any)
		if oldIsMap && newIsMap {
			changes = append(changes, diffMaps(path, oldMap, newMap)...)
		} else if !reflect.DeepEqual(oldVal, newVal) {
			changes = append(changes, fmt.Sprintf("~ %s: %v → %v", path, oldVal, newVal))
		}
	}

	for key := range old {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if _, exists := new_[key]; !exists {
			changes = append(changes, fmt.Sprintf("- %s", path))
		}
	}

	return changes
}

func newDiffCmd() *cobra.Command {
	var file string

	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Show pending configuration changes",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			if file == "" {
				return fmt.Errorf("--file is required: citeck diff -f <new-config.yml>")
			}

			currentYAML, err := c.GetConfig()
			if err != nil {
				return fmt.Errorf("fetch current config: %w", err)
			}

			newData, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("read file: %w", err)
			}

			var current, new_ map[string]any
			if err := yaml.Unmarshal([]byte(currentYAML), &current); err != nil {
				return fmt.Errorf("parse current config: %w", err)
			}
			if err := yaml.Unmarshal(newData, &new_); err != nil {
				return fmt.Errorf("parse new config: %w", err)
			}

			changes := diffMaps("", current, new_)

			output.PrintResult(map[string]any{
				"file":    file,
				"changes": changes,
			}, func() {
				if len(changes) == 0 {
					output.PrintText("No changes.")
				} else {
					output.PrintText("Changes:")
					for _, ch := range changes {
						output.PrintText("  %s", ch)
					}
				}
			})
			return nil
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to new namespace.yml to compare")

	return cmd
}

func newWaitCmd() *cobra.Command {
	var (
		status  string
		app     string
		healthy bool
		timeout int
	)

	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait for a condition",
		Long:  "Block until a condition is met. Like kubectl wait.",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				return err
			}
			defer c.Close()

			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
			defer cancel()

			// Check initial state
			if healthy {
				if h, err := c.GetHealth(); err == nil && h.Healthy {
					output.PrintText("Healthy")
					return nil
				}
			} else if ns, err := c.GetNamespace(); err == nil {
				if app != "" {
					for _, a := range ns.Apps {
						if a.Name == app && matchStatus(a.Status, status) {
							output.PrintText("App %s: %s", app, a.Status)
							return nil
						}
					}
				} else if matchStatus(ns.Status, status) {
					output.PrintText("Namespace: %s", ns.Status)
					return nil
				}
			}

			// Use SSE events for instant notification
			events, err := c.StreamEvents(ctx)
			if err != nil {
				return fmt.Errorf("connect to event stream: %w", err)
			}

			for {
				select {
				case <-ctx.Done():
					return exitWithCode(ExitTimeout, "timeout waiting for status")
				case evt, ok := <-events:
					if !ok {
						return fmt.Errorf("event stream closed")
					}
					if healthy && evt.Type == "namespace_status" && evt.After == "RUNNING" {
						output.PrintText("Healthy")
						return nil
					}
					if app != "" && evt.Type == "app_status" && evt.AppName == app && matchStatus(evt.After, status) {
						output.PrintText("App %s: %s", app, evt.After)
						return nil
					}
					if app == "" && evt.Type == "namespace_status" && matchStatus(evt.After, status) {
						output.PrintText("Namespace: %s", evt.After)
						return nil
					}
				}
			}
		},
	}

	cmd.Flags().StringVar(&status, "status", "RUNNING", "Status to wait for")
	cmd.Flags().StringVar(&app, "app", "", "Wait for specific app")
	cmd.Flags().BoolVar(&healthy, "healthy", false, "Wait for healthy status")
	cmd.Flags().IntVar(&timeout, "timeout", 300, "Timeout in seconds")

	return cmd
}

func matchStatus(actual, expected string) bool {
	return actual == expected
}
