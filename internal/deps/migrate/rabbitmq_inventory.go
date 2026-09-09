package migrate

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

// rabbitInventory is what a RabbitMQ upgrade must carry across: the topology
// the vendor's own verification advice names (users, virtual hosts, queues
// with their durability and message counts, exchanges, bindings, policies and
// parameters).
//
// Message COUNTS are in it for the reason the whole plan exists: RabbitMQ's
// logical export carries the topology and no messages at all, so a migration
// that moved every queue and lost what was in them would pass a topology-only
// comparison unnoticed.
type rabbitInventory struct {
	Users      []string
	Vhosts     []string
	Queues     map[string]rabbitQueue
	Exchanges  []string
	Bindings   []string
	Policies   []string
	Parameters []string
}

// rabbitQueue is one queue's identity beyond its name. Every field is the
// broker's own raw text: parsing "messages" into a number would turn a value
// this launcher does not understand (a future queue type reporting something
// else) into a broken inventory, while comparing the text catches a changed
// count exactly as well.
type rabbitQueue struct {
	Durable  string
	Type     string
	Messages string
}

// readRabbitInventory lists what the node in container holds.
//
// Every command is scoped to a virtual host except the two global ones, so the
// vhost list is read first and drives the rest — which is also what makes a
// LOST VHOST a single clear problem rather than a hundred missing queues.
func readRabbitInventory(ctx context.Context, env Env, container string) (Inventory, error) {
	inv := rabbitInventory{Queues: map[string]rabbitQueue{}}

	users, err := rabbitRows(ctx, env, container, "list_users")
	if err != nil {
		return inv, err
	}
	// list_users takes no column arguments at all (verified against a real
	// 4.1 broker: "Error (argument validation): too many arguments"), so the
	// name is the first cell of "name<TAB>tags".
	for _, row := range users {
		inv.Users = append(inv.Users, cell(row, 0))
	}
	vhosts, err := rabbitRows(ctx, env, container, "list_vhosts", "name")
	if err != nil {
		return inv, err
	}
	for _, row := range vhosts {
		inv.Vhosts = append(inv.Vhosts, cell(row, 0))
	}
	for _, vhost := range inv.Vhosts {
		if err := inv.readVhost(ctx, env, container, vhost); err != nil {
			return inv, err
		}
	}
	inv.sort()
	return inv, nil
}

// readVhost reads everything that lives inside one virtual host.
func (inv *rabbitInventory) readVhost(ctx context.Context, env Env, container, vhost string) error {
	queues, err := rabbitRows(ctx, env, container, "list_queues", "--vhost", vhost,
		"name", "durable", "type", "messages")
	if err != nil {
		return err
	}
	for _, row := range queues {
		inv.Queues[qualified(vhost, cell(row, 0))] = rabbitQueue{
			Durable: cell(row, 1), Type: cell(row, 2), Messages: cell(row, 3),
		}
	}
	exchanges, err := rabbitRows(ctx, env, container, "list_exchanges", "--vhost", vhost,
		"name", "type", "durable")
	if err != nil {
		return err
	}
	for _, row := range exchanges {
		inv.Exchanges = append(inv.Exchanges, fmt.Sprintf("%s (%s, durable %s)",
			qualified(vhost, cell(row, 0)), cell(row, 1), cell(row, 2)))
	}
	bindings, err := rabbitRows(ctx, env, container, "list_bindings", "--vhost", vhost,
		"source_name", "destination_name", "destination_kind", "routing_key")
	if err != nil {
		return err
	}
	for _, row := range bindings {
		inv.Bindings = append(inv.Bindings, fmt.Sprintf("%s → %s %q (key %q)",
			qualified(vhost, cell(row, 0)), cell(row, 2), cell(row, 1), cell(row, 3)))
	}
	// list_policies and list_parameters are read with their DEFAULT columns:
	// both already start with the virtual host, and naming columns explicitly
	// buys nothing while risking a column name a future release renames.
	policies, err := rabbitRows(ctx, env, container, "list_policies", "--vhost", vhost)
	if err != nil {
		return err
	}
	for _, row := range policies {
		inv.Policies = append(inv.Policies, strings.Join(row, " "))
	}
	parameters, err := rabbitRows(ctx, env, container, "list_parameters", "--vhost", vhost)
	if err != nil {
		return err
	}
	for _, row := range parameters {
		inv.Parameters = append(inv.Parameters, qualified(vhost, strings.Join(row, " ")))
	}
	return nil
}

func (inv *rabbitInventory) sort() {
	for _, s := range [][]string{inv.Users, inv.Vhosts, inv.Exchanges, inv.Bindings, inv.Policies, inv.Parameters} {
		sort.Strings(s)
	}
}

// qualified names an object by its virtual host. The name is QUOTED because
// RabbitMQ's default exchange has an empty one, and "/ " reads as a mistake
// where `/ ""` reads as the default exchange.
func qualified(vhost, name string) string { return fmt.Sprintf("%s %q", vhost, name) }

// Diff compares the node before the upgrade with the node after it.
//
// Anything MISSING fails the verify; anything that appeared is a note (see
// inventory_sets.go for why the two are not symmetric). A queue that survived
// is compared field by field, and a changed message count is a problem in
// either direction: nothing is connected to either node, so a count that moved
// at all means the upgrade moved it.
func (inv rabbitInventory) Diff(after Inventory) (problems, notes []string) {
	b, ok := after.(rabbitInventory)
	if !ok {
		return []string{"the inventory of the upgraded node could not be compared with the original"}, nil
	}
	for _, set := range []struct {
		kind          string
		before, after []string
	}{
		{"user(s)", inv.Users, b.Users},
		{"virtual host(s)", inv.Vhosts, b.Vhosts},
		{"exchange(s)", inv.Exchanges, b.Exchanges},
		{"binding(s)", inv.Bindings, b.Bindings},
		{"polic(ies)", inv.Policies, b.Policies},
		{"parameter(s)", inv.Parameters, b.Parameters},
	} {
		p, n := lostAndFound(set.kind, set.before, set.after)
		problems, notes = append(problems, p...), append(notes, n...)
	}
	qp, qn := diffQueues(inv.Queues, b.Queues)
	return append(problems, qp...), append(notes, qn...)
}

// diffQueues compares the queues by name and then by content.
func diffQueues(before, after map[string]rabbitQueue) (problems, notes []string) {
	problems, notes = lostAndFound("queue(s)", slices.Sorted(maps.Keys(before)), slices.Sorted(maps.Keys(after)))
	for _, name := range slices.Sorted(maps.Keys(before)) {
		was, is := before[name], after[name]
		if _, ok := after[name]; !ok {
			continue // already reported as missing
		}
		if was.Durable != is.Durable || was.Type != is.Type {
			problems = append(problems, fmt.Sprintf(
				"queue %s changed: was %s/durable %s, is %s/durable %s",
				name, was.Type, was.Durable, is.Type, is.Durable))
		}
		if was.Messages != is.Messages {
			problems = append(problems, fmt.Sprintf(
				"queue %s held %s message(s) before the upgrade and %s after", name, was.Messages, is.Messages))
		}
	}
	return problems, notes
}

// rabbitRows runs a rabbitmqctl listing and returns its rows.
//
// -q suppresses the informational banner and --no-table-headers the header
// row; both were verified against a real 4.1 broker, as was every column name
// used above.
func rabbitRows(ctx context.Context, env Env, container string, args ...string) ([][]string, error) {
	cmd := append([]string{"rabbitmqctl", "-q", "--no-table-headers"}, args...)
	stdout, stderr, code, err := env.Exec(ctx, container, cmd)
	if err != nil {
		return nil, fmt.Errorf("%s in %s: %w", args[0], container, err)
	}
	if code != 0 {
		return nil, fmt.Errorf("%s in %s: exit %d: %s", args[0], container, code, tail(stderr))
	}
	return tabRows(stdout), nil
}

// tabRows splits a listing into TAB-separated rows.
//
// A line is NOT trimmed before it is split: RabbitMQ's default exchange has an
// EMPTY name, so its row really is "\tdirect\ttrue" (measured), and trimming
// would shift every column of it one to the left — reading the exchange's TYPE
// as its name, silently, on every namespace.
func tabRows(stdout string) [][]string {
	var rows [][]string
	for line := range strings.SplitSeq(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	return rows
}

// cell reads a column that a listing may not have printed at all.
func cell(row []string, i int) string {
	if i < len(row) {
		return row[i]
	}
	return ""
}
