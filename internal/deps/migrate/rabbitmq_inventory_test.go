package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
)

// The output below is what a real rabbitmq:4.1.2-management broker printed for
// these exact commands (verified 2026-09-09), tabs included. The default
// exchange's row is the one that matters: its name is EMPTY, so the row begins
// with a TAB and a parser that trims the line before splitting it reads
// "direct" as the exchange's name.
const (
	realUsersOut     = "guest\t[administrator]\nprobe\t[]\n"
	realVhostsOut    = "probevhost\n/\n"
	realExchangesOut = "amq.headers\theaders\ttrue\n" +
		"amq.topic\ttopic\ttrue\n" +
		"\tdirect\ttrue\n" +
		"amq.direct\tdirect\ttrue\n"
	realQueuesOut   = "citeck.events\ttrue\tquorum\t42\nalerts\ttrue\tclassic\t0\n"
	realBindingsOut = "amq.direct\tciteck.events\tqueue\tevents\n"
	realPoliciesOut = "probevhost\tpol\t^p\tall\t{\"max-length\":10}\t0\n"
	realParamsOut   = "\n" // list_parameters prints a blank line when there are none
)

// rabbitInventoryEnv is a fake with one running broker whose commands the
// script answers.
func rabbitInventoryEnv(t *testing.T, s *execScript) *migratetest.FakeEnv {
	t.Helper()
	f := migratetest.New()
	f.Containers[SrcContainer] = appdef.ApplicationDef{Name: appdef.AppRabbitmq, Image: rabbitFrom}
	f.ExecFn = s.exec
	return f
}

func readRabbit(t *testing.T, s *execScript) rabbitInventory {
	t.Helper()
	inv, err := readRabbitInventory(context.Background(), rabbitInventoryEnv(t, s), SrcContainer)
	require.NoError(t, err)
	r, ok := inv.(rabbitInventory)
	require.True(t, ok)
	return r
}

// The parser is pinned against a real broker's output, and the case it exists
// for is the default exchange: RabbitMQ names it with an EMPTY string, so its
// row starts with a tab.
func TestRabbitInventoryParsesARealBrokersOutput(t *testing.T) {
	inv := readRabbit(t, &execScript{out: map[string]string{
		SrcContainer + "|list_users":      realUsersOut,
		SrcContainer + "|list_vhosts":     realVhostsOut,
		SrcContainer + "|list_exchanges":  realExchangesOut,
		SrcContainer + "|list_queues":     realQueuesOut,
		SrcContainer + "|list_bindings":   realBindingsOut,
		SrcContainer + "|list_policies":   realPoliciesOut,
		SrcContainer + "|list_parameters": realParamsOut,
	}})
	assert.Equal(t, []string{"guest", "probe"}, inv.Users, "the name is the first column, not the whole row")
	assert.Equal(t, []string{"/", "probevhost"}, inv.Vhosts)
	assert.Contains(t, inv.Exchanges, `/ "" (direct, durable true)`,
		"the default exchange has an empty name; trimming the row would read its TYPE as its name")
	assert.Contains(t, inv.Exchanges, `/ "amq.topic" (topic, durable true)`)
	assert.Equal(t, rabbitQueue{Durable: "true", Type: "quorum", Messages: "42"},
		inv.Queues[`/ "citeck.events"`])
	assert.Contains(t, inv.Bindings, `/ "amq.direct" → queue "citeck.events" (key "events")`)
	assert.Contains(t, inv.Policies, `probevhost pol ^p all {"max-length":10} 0`)
	assert.Empty(t, inv.Parameters, "a blank line is not a parameter")

	// Every vhost is read, not just the first: a per-vhost listing that only
	// ever saw "/" would report an empty probevhost as intact.
	assert.Len(t, inv.Queues, 4, "two queues per vhost, because the script answers both the same way")
}

// A command that fails is an inventory that could not be read, and the step
// has to fail rather than compare against a picture with holes in it.
func TestRabbitInventoryFailsWhenACommandFails(t *testing.T) {
	env := rabbitInventoryEnv(t, &execScript{
		out:  map[string]string{SrcContainer + "|list_vhosts": realVhostsOut},
		fail: map[string]string{SrcContainer + "|list_queues": "Error: unable to connect to node rabbit@rabbitmq"},
	})
	_, err := readRabbitInventory(context.Background(), env, SrcContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to connect")
}

// What the verify is for: a lost object, a lost user and a message count that
// moved are all failures, and each one names itself.
func TestRabbitInventoryDiffCatchesALostQueueAUserAndAChangedMessageCount(t *testing.T) {
	before := rabbitInventory{
		Users:  []string{"citeck", "guest"},
		Vhosts: []string{"/"},
		Queues: map[string]rabbitQueue{
			`/ "citeck.events"`: {Durable: "true", Type: "quorum", Messages: "42"},
			`/ "alerts"`:        {Durable: "true", Type: "classic", Messages: "0"},
		},
		Exchanges: []string{`/ "amq.direct" (direct, durable true)`},
	}
	after := rabbitInventory{
		Users:  []string{"guest"},
		Vhosts: []string{"/"},
		Queues: map[string]rabbitQueue{
			`/ "citeck.events"`: {Durable: "true", Type: "quorum", Messages: "41"},
		},
		Exchanges: []string{`/ "amq.direct" (direct, durable true)`},
	}
	problems, notes := before.Diff(after)
	joined := strings.Join(problems, "\n")
	assert.Contains(t, joined, "1 user(s) missing after the upgrade: citeck")
	assert.Contains(t, joined, `1 queue(s) missing after the upgrade: / "alerts"`)
	assert.Contains(t, joined, `queue / "citeck.events" held 42 message(s) before the upgrade and 41 after`)
	assert.Empty(t, notes)
}

// A queue that survived but changed shape is a problem too — a durable queue
// that came back transient is a queue whose messages the next restart loses.
func TestRabbitInventoryDiffCatchesAQueueThatChangedShape(t *testing.T) {
	before := rabbitInventory{Queues: map[string]rabbitQueue{
		`/ "alerts"`: {Durable: "true", Type: "quorum", Messages: "1"},
	}}
	after := rabbitInventory{Queues: map[string]rabbitQueue{
		`/ "alerts"`: {Durable: "false", Type: "classic", Messages: "1"},
	}}
	problems, _ := before.Diff(after)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "was quorum/durable true, is classic/durable false")
}

// An object that APPEARED is reported and nothing more. Nothing is connected
// to the copy, so a new object is the new version's own — a newer RabbitMQ
// that ships another internal exchange must not fail a migration that lost
// nothing.
func TestRabbitInventoryDiffReportsNewObjectsWithoutFailing(t *testing.T) {
	before := rabbitInventory{Users: []string{"guest"}}
	after := rabbitInventory{Users: []string{"guest", "rabbitmq-internal"}}
	problems, notes := before.Diff(after)
	assert.Empty(t, problems)
	require.Len(t, notes, 1)
	assert.Contains(t, notes[0], "rabbitmq-internal")
}

// A long list stays readable and still says how big it is.
func TestInventoryNamesPreviewCapsTheListButNotTheCount(t *testing.T) {
	names := make([]string, 0, maxNamedItems+5)
	for i := range maxNamedItems + 5 {
		names = append(names, string(rune('a'+i%26))+"-queue")
	}
	problems, _ := lostAndFound("queue(s)", names, nil)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "25 queue(s) missing")
	assert.Contains(t, problems[0], "and 5 more")
}

// The verify compares two pictures of the same kind. A type that is not one
// is a bug in the plan, and reporting it as "nothing changed" would pass a
// migration nobody checked.
func TestRabbitInventoryDiffRefusesAForeignInventory(t *testing.T) {
	problems, notes := rabbitInventory{}.Diff(countInventory{})
	assert.NotEmpty(t, problems)
	assert.Empty(t, notes)
}
