package cli

import (
	"fmt"
	"net"
	"net/url"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/desktop"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

// newUICmd prints (and tries to open) the Web UI URL. When daemon.yml
// api_auth is enabled, the URL is the GET /auth/session handshake link
// carrying the API token, so the browser lands already authenticated (the
// daemon answers with an HttpOnly session cookie and redirects to /). The
// token is read from daemon.yml api_auth.token or the daemon-generated
// conf/api-token file — readable only by users the file mode (0600) allows,
// which is exactly the access-control contract.
func newUICmd() *cobra.Command {
	var noOpen bool
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Open the Web UI (prints an authenticated link when API token auth is enabled)",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.LoadDaemonConfig()
			if err != nil {
				return fmt.Errorf("load daemon config: %w", err)
			}
			if !cfg.Server.WebUI.Enabled {
				return fmt.Errorf("web UI is disabled — it is off by default in server mode (the CLI/TUI is the supported interface); "+
					"to opt in, set server.webui.enabled: true in %s (recommended together with api_auth.enabled: true) and restart the daemon",
					config.DaemonConfigPath())
			}
			uiURL, err := webUIURL(cfg)
			if err != nil {
				return err
			}
			if output.IsJSON() {
				output.PrintJSON(map[string]any{"url": uiURL, "tokenAuth": cfg.APIAuth.Enabled})
			} else {
				output.PrintText("Web UI: %s", uiURL)
				if cfg.APIAuth.Enabled {
					output.PrintText("API token auth is enabled — the link above opens an authenticated browser session.")
				}
			}
			if !noOpen {
				// Best-effort: xdg-open / open / rundll32 when a desktop
				// environment is around; on a headless server this silently
				// fails and the printed URL is the result.
				_ = desktop.OpenBrowser(uiURL)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "Only print the URL, don't try to launch a browser")
	return cmd
}

// webUIURL builds the local Web UI URL from daemon.yml. With api_auth
// enabled it returns the /auth/session handshake link carrying the token;
// otherwise the plain base URL. A wildcard bind (empty / 0.0.0.0 / ::) maps
// to 127.0.0.1 — this command serves the local-browser flow; remote access
// goes through mTLS and never needs the token.
func webUIURL(cfg config.DaemonConfig) (string, error) {
	host, port, err := net.SplitHostPort(cfg.Server.WebUI.Listen)
	if err != nil {
		return "", fmt.Errorf("invalid webui listen address %q: %w", cfg.Server.WebUI.Listen, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	base := "http://" + net.JoinHostPort(host, port)
	if !cfg.APIAuth.Enabled {
		return base + "/", nil
	}
	token, err := config.LoadAPIToken(cfg)
	if err != nil {
		return "", fmt.Errorf("api_auth is enabled but no token is readable (the daemon writes %s on start; check it exists and you may read it): %w",
			config.APITokenPath(), err)
	}
	return base + api.AuthSession + "?token=" + url.QueryEscape(token), nil
}
