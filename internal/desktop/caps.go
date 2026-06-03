package desktop

import "encoding/json"

// CapsContractVersion is bumped when the verb set / semantics change in a way
// the backend must detect. Backend feature-detects via Supports().
const CapsContractVersion = 1

// Native verb names the wrapper advertises and executes. Add new verbs here AND
// implement them in the wrapper; the backend gates use on Supports().
const (
	VerbWindowShow     = "window.show"
	VerbWindowHide     = "window.hide"
	VerbWindowFocus    = "window.focus"
	VerbWindowMinimize = "window.minimize"
	VerbWindowReload   = "window.reload"
	VerbWindowNavigate = "window.navigate"
	VerbWindowSetTitle = "window.setTitle"
	VerbAppQuit        = "app.quit"
	VerbAppRelaunch    = "app.relaunch"
	VerbDevtoolsOpen   = "devtools.open"
	VerbShellOpenPath  = "shell.openPath"
	VerbShellOpenURL   = "shell.openURL"
	VerbClipboardWrite = "clipboard.writeText"
	VerbClipboardRead  = "clipboard.readText"
	VerbDialogMessage  = "dialog.message"
	VerbDialogConfirm  = "dialog.confirm"
	VerbDialogOpenFile = "dialog.openFile"
	VerbDialogSaveFile = "dialog.saveFile"
	VerbNotifyShow     = "notify.show"
	VerbTraySetMenu    = "tray.setMenu"
	VerbTraySetIcon    = "tray.setIcon"
	VerbTraySetTooltip = "tray.setTooltip"
	VerbAutostartSet   = "autostart.set"
	VerbAutostartGet   = "autostart.get"
)

// Capabilities is what the wrapper advertises to the daemon (env CITECK_WRAPPER_CAPS).
type Capabilities struct {
	ContractVersion int      `json:"contractVersion"`
	Verbs           []string `json:"verbs"`
}

// Encode marshals capabilities for the CITECK_WRAPPER_CAPS env var.
func (c Capabilities) Encode() string {
	b, _ := json.Marshal(c) //nolint:errchkjson // fixed struct, cannot fail
	return string(b)
}

// Supports reports whether the wrapper advertised the given verb.
func (c Capabilities) Supports(verb string) bool {
	for _, v := range c.Verbs {
		if v == verb {
			return true
		}
	}
	return false
}

// ParseCapabilities reads CITECK_WRAPPER_CAPS; empty string yields empty caps.
func ParseCapabilities(env string) (Capabilities, error) {
	if env == "" {
		return Capabilities{}, nil
	}
	var c Capabilities
	if err := json.Unmarshal([]byte(env), &c); err != nil {
		return Capabilities{}, err //nolint:wrapcheck // caller logs
	}
	return c, nil
}
