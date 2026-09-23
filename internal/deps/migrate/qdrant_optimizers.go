package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/msg"
)

// qdrantOptimizerTimeout bounds how long a node that is about to be replaced
// may keep optimizing, and qdrantOptimizerPoll is how often it is asked.
// Vars, not consts, so a test can shrink them (save, reassign,
// defer-restore) — the same arrangement as dumpProgressPoll.
//
// The timeout is generous because an optimization that really runs is an
// index build, which on a large collection takes minutes and not seconds —
// and the cost of running out is only a rolled-back migration: the
// namespace's own volume has not been touched.
var (
	qdrantOptimizerTimeout = 30 * time.Minute
	qdrantOptimizerPoll    = 5 * time.Second
)

// waitForQdrantOptimizers is Qdrant's pre-upgrade hook: before the node on the
// copy is replaced by the next rung, no collection it serves may have an
// optimization RUNNING, and none may report an optimizer ERROR.
//
// That is the vendor's own rule for a stepwise upgrade — settle on one minor
// before moving to the next — and the step where it has a concrete reason is
// 1.16 → 1.17: 1.16 moves RocksDB-backed storage into Gridstore, and 1.17
// reads no RocksDB at all ("unsupported storage"). It is a PRE-upgrade hook
// because that is where the shared plan asks the node that is about to be
// replaced, on every rung, the bottom one included; the top rung is replaced
// by nothing, so nothing is asked of it there.
//
// Measured on real containers (2026-09-23), a volume written by 1.14.1 walked
// 1.15.5 → 1.16.3 → 1.17.1 → 1.18.3 → 1.19.1 with every node stopped the
// moment it answered /readyz, and lost nothing: 1.16 moves the payload
// indices while the segments LOAD, i.e. before readiness. So on that data
// this wait answered at once on every rung. It is here for what readiness
// does not say — an optimization actually running, and an optimizer that
// failed — and it is cheap when neither is the case: one request per
// collection.
//
// What it deliberately does NOT wait for is "green". A collection whose
// optimizations were pending when its server stopped reads "grey" after
// every boot — pending but PAUSED until an update arrives (measured on
// 1.14.1, 1.15.5 and 1.16.3, the same collection stopped mid-index-build) —
// and nothing writes to a temp container, so a wait for green would hang
// every such migration until its deadline. Nor does it send the update that
// would resume them: that is an index build the upgrade does not need, and
// the volume above (grey on every rung) went through 1.17 intact without it.
func waitForQdrantOptimizers(ctx context.Context, env Env, container string, p StepProgress) error {
	var busy []string
	err := pollUntil(ctx, env, container, qdrantOptimizerTimeout, qdrantOptimizerPoll,
		func() msg.Message {
			return msg.New("deps.msg.progress.optimizing",
				"container", container, "collections", listPreview(busy))
		},
		func(ctx context.Context) (bool, error) {
			var err error
			busy, err = qdrantBusyCollections(ctx, env, container)
			return err == nil && len(busy) == 0, err
		},
		p)
	if errors.Is(err, errPollDeadline) {
		return fmt.Errorf("the Qdrant server in %s is still optimizing %s after %s",
			container, namesPreview(busy), qdrantOptimizerTimeout)
	}
	return err
}

// qdrantOptimizerState is the subset of GET /collections/{name} this wait
// reads. OptimizerStatus is raw because the schema is a oneOf: the string
// "ok", or an object {"error": "..."} (OptimizersStatus in Qdrant's OpenAPI).
type qdrantOptimizerState struct {
	Result struct {
		Status          string          `json:"status"`
		OptimizerStatus json.RawMessage `json:"optimizer_status"`
	} `json:"result"`
}

// qdrantBusyCollections answers the collections still optimizing, sorted, or
// an error for a server that will not settle by being asked again.
//
// A request that fails is such an error too, and deliberately not a retry:
// the server answered /readyz a moment ago, and the inventory read right
// after this wait treats the same failure as fatal — waiting half an hour to
// arrive at the verdict it would reach anyway helps nobody.
func qdrantBusyCollections(ctx context.Context, env Env, container string) ([]string, error) {
	var list qdrantCollections
	if err := qdrantGetJSON(ctx, env, container, "/collections", &list); err != nil {
		return nil, err
	}
	var busy []string
	for _, c := range list.Result.Collections {
		var state qdrantOptimizerState
		if err := qdrantGetJSON(ctx, env, container, "/collections/"+c.Name, &state); err != nil {
			return nil, err
		}
		settled, err := state.settled()
		if err != nil {
			return nil, fmt.Errorf("collection %q of %s: %w", c.Name, container, err)
		}
		if !settled {
			busy = append(busy, c.Name)
		}
	}
	sort.Strings(busy)
	return busy, nil
}

// settled reads one collection's state.
//
// The status set is Qdrant's own enum (CollectionStatus): green and grey are
// settled — grey only means "pending, paused", see waitForQdrantOptimizers —
// yellow is an optimization running, red is "an operation failed and was not
// recovered". Anything else is refused rather than guessed at: a status this
// launcher does not know is not evidence that the data is safe to hand to the
// next minor.
func (s qdrantOptimizerState) settled() (bool, error) {
	if err := s.optimizerError(); err != nil {
		return false, err
	}
	switch s.Result.Status {
	case "green", "grey":
		return true, nil
	case "yellow":
		return false, nil
	case "red":
		return false, errors.New(`status "red": an operation failed and was not recovered`)
	default:
		return false, fmt.Errorf("status %q, which this launcher does not know", s.Result.Status)
	}
}

// optimizerError answers nil for "ok" and an error for everything else —
// including a value of neither documented shape, which is refused for the
// same reason an unknown status is.
func (s qdrantOptimizerState) optimizerError() error {
	var ok string
	if err := json.Unmarshal(s.Result.OptimizerStatus, &ok); err == nil {
		if ok == "ok" {
			return nil
		}
		return fmt.Errorf("optimizer status %q, which this launcher does not know", ok)
	}
	var failed struct {
		Error *string `json:"error"`
	}
	if err := json.Unmarshal(s.Result.OptimizerStatus, &failed); err == nil && failed.Error != nil {
		return fmt.Errorf("the optimizer reports an error: %s", *failed.Error)
	}
	return fmt.Errorf("unreadable optimizer status %s", string(s.Result.OptimizerStatus))
}

// listPreview joins at most maxNamedItems names for a sentence that is
// TRANSLATED — unlike namesPreview, whose "and N more" is English and belongs
// in the errors and notes we read ourselves.
func listPreview(names []string) string {
	if len(names) <= maxNamedItems {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:maxNamedItems], ", ") + ", …"
}
