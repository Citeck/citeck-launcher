package cli

import (
	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/output"
)

// The operator-facing half of the persist-debt work.
//
// The runtime already retries a state write the store refused, and reports the
// streak once in the daemon log (internal/namespace/persist_retry.go). What it
// could not fix is AWARENESS: every mutator that records durable user intent —
// `citeck stop <app>`, `citeck start <app>`, `citeck restart <app>`,
// `citeck edit <app>` — answers success on a refused write, because the action
// itself succeeded. The container really stopped; the patch really applied to
// the live container. Only the RECORD of it was refused, and the operator found
// out at the next daemon start, where the app was un-detached again and the
// edit was gone.
//
// So the condition rides on the namespace DTO (api.NamespaceDto.StateWriteError)
// and every surface reads it from there. This file is the CLI's share of that:
// one line on `citeck status`, one warning after a per-app action, and the
// refusal that stops a binary upgrade from being layered on top of a lost state.

// stateWriteStatusLine renders the value of the `citeck status` "State:" line,
// or "" when the namespace's writes are landing (the overwhelmingly common
// case, where the line is omitted entirely — like "License:" on an older
// daemon).
func stateWriteStatusLine(ns *api.NamespaceDto) string {
	if ns == nil || ns.StateWriteError == "" {
		return ""
	}
	return output.Colorize(output.Red, t("cli.stateWrite.statusLine", "err", ns.StateWriteError))
}

// stateWriteChecker is the daemon surface a per-app action command needs on top
// of its own result: enough to tell "applied" from "applied and recorded".
// Narrowed to the one call so the policy is testable without a daemon.
type stateWriteChecker interface {
	GetNamespace() (*api.NamespaceDto, error)
}

// appliedButNotSavedWarning returns the lines to print after an action that
// succeeded but may not have been recorded, or nil when the store is healthy.
//
// Best-effort on purpose. It runs AFTER an action that already succeeded and
// was already reported, so an unreachable daemon, an older one with no such
// field, or a namespace that is not configured must not turn a successful
// command into a failed one — each of those simply produces no warning. The
// action's exit code is never touched.
func appliedButNotSavedWarning(c stateWriteChecker) []string {
	ns, err := c.GetNamespace()
	if err != nil || ns == nil || ns.StateWriteError == "" {
		return nil
	}
	return []string{
		t("cli.stateWrite.actionWarning", "err", ns.StateWriteError),
		t("cli.stateWrite.actionHint"),
	}
}

// warnIfStateNotSaved prints appliedButNotSavedWarning to STDERR.
//
// stderr rather than stdout for two reasons: `--format json` writes the action
// result to stdout and a stray text line would corrupt it for a scripted
// caller, and this is a warning about something that already happened rather
// than part of the command's answer.
func warnIfStateNotSaved(c stateWriteChecker) {
	ensureI18n()
	for _, line := range appliedButNotSavedWarning(c) {
		output.Errf("%s", line)
	}
}

// stateNotSavedError marks a leave-running shutdown that detached the daemon —
// the containers are running, as asked — but could not write the namespace
// state the next daemon will adopt them with.
type stateNotSavedError struct{ detail string }

func (e *stateNotSavedError) Error() string {
	return "namespace state was not saved on detach: " + e.detail
}

// detachStateError turns a leave-running shutdown response into that sentinel.
// A response that carries no such field is NOT evidence of a loss — it is what
// a daemon older than this field answers — so it maps to nil.
func detachStateError(res *api.ActionResultDto) error {
	if res == nil || res.StateSaveError == "" {
		return nil
	}
	return &stateNotSavedError{detail: res.StateSaveError}
}

// stateNotSavedAbortLines is what a binary upgrade prints instead of swapping
// the binary.
//
// Aborting undoes nothing: by the time the write fails the daemon is already
// down and the containers are already detached. Its value is that the operator
// is told BEFORE a version change is layered on top of a state that was already
// lost, and that restarting the SAME binary — which re-adopts the running
// containers — is the safe next step. So the text has to carry three things:
// the cause, what the next daemon would get wrong, and what to do about it.
func stateNotSavedAbortLines(detail string) []string {
	return []string{
		"",
		output.Colorize(output.Red, t("install.lifecycle.upgrade.stateNotSaved")),
		"  " + t("install.lifecycle.upgrade.stateNotSavedCause", "err", detail),
		"  " + t("install.lifecycle.upgrade.stateNotSavedStale"),
		"  " + t("install.lifecycle.upgrade.stateNotSavedNext", "path", installTarget),
		"",
	}
}
