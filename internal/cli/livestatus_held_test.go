package cli

import (
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/stretchr/testify/assert"
)

// A wait that ends with apps HELD by a dependency the user stopped must not
// print "All apps started." — `citeck stop zookeeper` leaves gateway and proxy
// in DEPS_WAITING and the stand answers nothing, which is the opposite of what
// that line says. The detached apps are the user's own choice; the held ones
// are a second-order consequence of it, and this is the only place they are
// ever mentioned.
func TestTerminalStartMessage_NamesTheHoldInsteadOfClaimingSuccess(t *testing.T) {
	ensureI18n()

	msg := terminalStartMessage(2, 9, []string{"zookeeper"}, "")

	assert.NotContains(t, msg, "All apps started")
	assert.Contains(t, msg, "zookeeper", "оператору нужно имя того, что запустить")
	assert.Contains(t, msg, "2")
}

// With nothing held the message is unchanged — the caller's own success text
// when it has one, the shared "all started" line otherwise.
func TestTerminalStartMessage_UnchangedWhenNothingIsHeld(t *testing.T) {
	ensureI18n()

	assert.Equal(t, "reload done", terminalStartMessage(0, 9, nil, "reload done"))
	assert.True(t, strings.Contains(terminalStartMessage(0, 9, nil, ""), "started") ||
		strings.Contains(terminalStartMessage(0, 9, nil, ""), "запущены"),
		"без удержаний печатается обычная строка успеха")
}

// `citeck start <app>` used to name the detached roots of the WHOLE namespace.
// With two independent ones — `citeck stop postgres` and `citeck stop
// onlyoffice` — waiting on emodel told the operator to start onlyoffice, which
// releases nothing. The loop's own tests cannot see this: they assert that the
// wait ENDS, and the defect was in which name it printed on the way out.
func TestSingleAppHeldMessage_NamesOnlyThisAppsRoot(t *testing.T) {
	ensureI18n()
	apps := []api.AppDto{
		{Name: "postgres", Status: "STOPPED"},
		{Name: "onlyoffice", Status: "STOPPED"},
		{Name: "emodel", Status: "DEPS_WAITING", Held: true,
			WaitingFor: []api.WaitingDepDto{{App: "postgres", Status: "STOPPED"}}},
		{Name: "proxy", Status: "DEPS_WAITING", Held: true,
			WaitingFor: []api.WaitingDepDto{{App: "onlyoffice", Status: "STOPPED"}}},
	}

	msg := singleAppHeldMessage(apps, "emodel")

	assert.Contains(t, msg, "emodel", "сообщение про то приложение, которого ждут")
	assert.Contains(t, msg, "postgres", "запустить нужно именно его")
	assert.NotContains(t, msg, "onlyoffice",
		"onlyoffice держит другое приложение — запуск его для emodel бесполезен")
}

// The reason the namespace-wide call was here at all: on a transitive hold the
// app's own WaitingFor names an intermediate held app the operator never
// stopped and cannot start. The message must still come out at the root.
func TestSingleAppHeldMessage_WalksThroughAnIntermediateHold(t *testing.T) {
	ensureI18n()
	apps := []api.AppDto{
		{Name: "zookeeper", Status: "STOPPED"},
		{Name: "gateway", Status: "DEPS_WAITING", Held: true,
			WaitingFor: []api.WaitingDepDto{{App: "zookeeper", Status: "STOPPED"}}},
		{Name: "proxy", Status: "DEPS_WAITING", Held: true,
			WaitingFor: []api.WaitingDepDto{{App: "gateway", Status: "DEPS_WAITING"}}},
	}

	msg := singleAppHeldMessage(apps, "proxy")

	assert.Contains(t, msg, "zookeeper")
	assert.NotContains(t, msg, "gateway", "gateway запустить нельзя — он удерживается тем же правилом")
}
