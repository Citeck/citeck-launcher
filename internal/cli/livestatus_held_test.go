package cli

import (
	"strings"
	"testing"

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
