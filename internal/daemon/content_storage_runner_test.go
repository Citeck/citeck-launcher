package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/msg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runnerHistory struct {
	pending   string
	confirmed bool
}

func (h *runnerHistory) ContentStorageSwitch() string   { return h.pending }
func (h *runnerHistory) ContentStorageDefaultSet() bool { return h.confirmed }

// The connection protocol must run with no daemon, runtime or scheduler.
// Persistence happens before mutation and confirmation only after readback.
func TestCSConnectionRunnerIndependentProtocol(t *testing.T) {
	for _, durable := range []bool{false, true} {
		t.Run(map[bool]string{false: "WAL refused", true: "confirmed"}[durable], func(t *testing.T) {
			ec := newFakeECOS()
			history := &runnerHistory{}
			var order []string
			runner := csConnectionRunner{
				nsID: "test", state: history, now: time.Now, readyTimeout: time.Second,
				probe: func(ctx context.Context, ids csRecordIDs) (ecosRecords, csSnapshot, error) {
					snap, err := csReadSnapshot(ctx, ec, ids)
					return ec, snap, err
				},
				mayWrite: func(context.Context) bool { return true },
				recordSwitch: func(_ context.Context, ref string) bool {
					order = append(order, "WAL")
					if durable {
						history.pending = ref
					}
					return durable
				},
				recordVerdict: func(_ context.Context, connected bool) bool {
					require.True(t, connected)
					assert.Equal(t, csTestStorageRef, ec.defaultRef)
					order = append(order, "confirmed")
					history.pending = ""
					history.confirmed = true
					return true
				},
				provisioning: func() { order = append(order, "provisioning") },
				settle: func(state, ref string) {
					assert.Equal(t, api.ContentStorageStateConnected, state)
					assert.Equal(t, csTestStorageRef, ref)
				},
				fail: func(m msg.Message) { t.Fatalf("unexpected failure: %+v", m) },
				acquireMutation: func() (func(), bool) {
					order = append(order, "barrier")
					return func() { order = append(order, "released") }, true
				},
			}
			ec.onMutate = func(string) { assert.Equal(t, csTestStorageRef, history.pending); order = append(order, "mutate") }
			runner.run(context.Background())
			_, writes := ec.snapshot()
			if durable {
				require.Len(t, writes, 1)
				assert.Equal(t, []string{"WAL", "provisioning", "barrier", "mutate", "released", "confirmed"}, order)
				assert.True(t, history.confirmed)
			} else {
				assert.Empty(t, writes)
				assert.Equal(t, []string{"WAL"}, order)
				assert.False(t, history.confirmed)
			}
		})
	}
}
