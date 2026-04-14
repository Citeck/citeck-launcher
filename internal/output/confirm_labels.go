package output

// ConfirmYes and ConfirmNo hold localized button labels used by the
// prompt.Confirm primitive across every CLI/setup flow. They are set by
// the CLI i18n init (see internal/cli/i18n.go) and default to English
// so tools that run before i18n finishes loading still print readable
// button labels.
var (
	ConfirmYes = "Yes"
	ConfirmNo  = "No"
)
