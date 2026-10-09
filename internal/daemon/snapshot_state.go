package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/snapshot"
	"github.com/citeck/citeck-launcher/internal/storage"
)

type snapshotDestination struct {
	snapshot.SecretStore
	store      storage.Store
	svc        *storage.SecretService
	wsID, nsID string
	runtime    *namespace.Runtime
	infer      func(*snapshot.NamespaceSnapshotMeta) map[deps.ID]deps.DependencyState
}

// SnapshotDestination restores credentials and pins in one storage transaction.
// A nil runtime is the pre-activation create/install path.
func SnapshotDestination(store storage.Store, svc *storage.SecretService, wsID, nsID string, runtime *namespace.Runtime) snapshot.SecretStore {
	return &snapshotDestination{SecretStore: snapshot.NamespaceSecrets(svc, wsID, nsID), store: store, svc: svc, wsID: wsID, nsID: nsID, runtime: runtime}
}

func (d *Daemon) snapshotDestinationFor(ctx context.Context, act activeNamespace) snapshot.SecretStore {
	destination := SnapshotDestination(d.store, d.secretService, act.workspaceID, act.nsConfig.ID, act.runtime).(*snapshotDestination)
	if act.runtime != nil {
		destination.infer = func(meta *snapshot.NamespaceSnapshotMeta) map[deps.ID]deps.DependencyState {
			names := make([]string, 0, len(meta.Volumes))
			for _, v := range meta.Volumes {
				names = append(names, v.Name)
			}
			return snapshotDependencyPins(ctx, act.runtime.DependencyStates(), dockerDependencyProbe{dc: depsDockerOf(act.dockerClient), volumesBase: act.volumesBase}, names, namespaceDependencies(act.nsConfig, act.bundleDef, act.workspaceConfig))
		}
	}
	return destination
}

func (s *snapshotDestination) readState() (*namespace.NsPersistedState, error) {
	raw, have, err := s.store.LoadNamespaceState(s.wsID, s.nsID)
	if err != nil {
		return nil, fmt.Errorf("load snapshot destination state: %w", err)
	}
	state := &namespace.NsPersistedState{Status: namespace.NsStatusStopped}
	if have && json.Unmarshal([]byte(raw), state) != nil {
		return nil, fmt.Errorf("invalid snapshot destination state")
	}
	return state, nil
}

func (s *snapshotDestination) PrepareSnapshotRestore(meta *snapshot.NamespaceSnapshotMeta, values map[string]string) (*snapshot.RestorePlan, error) {
	if s.svc == nil || !s.svc.SnapshotTransactionsAvailable() {
		return nil, fmt.Errorf("snapshot metadata transactions unavailable")
	}
	if err := storage.RecoverSnapshotMetadata(s.store); err != nil {
		return nil, fmt.Errorf("recover destination snapshot metadata: %w", err)
	}
	if _, err := s.PrepareRestore(values); err != nil {
		return nil, fmt.Errorf("prepare snapshot credentials: %w", err)
	}
	if _, err := s.readState(); err != nil {
		return nil, err
	}
	commit := func(state *namespace.NsPersistedState) error {
		raw, err := json.Marshal(state)
		if err != nil {
			return fmt.Errorf("encode snapshot destination state: %w", err)
		}
		return s.svc.CommitNamespaceSnapshot(storage.SnapshotState{WorkspaceID: s.wsID, NamespaceID: s.nsID, Status: string(state.Status), JSON: string(raw)}, values)
	}
	return &snapshot.RestorePlan{
		Begin: func() error {
			if s.runtime != nil {
				return s.runtime.BeginSnapshotImport()
			}
			state, err := s.readState()
			if err != nil {
				return err
			}
			state.Status = namespace.NsStatusStopped
			state.SnapshotImportPending = true
			raw, err := json.Marshal(state)
			if err != nil {
				return fmt.Errorf("encode incomplete snapshot state: %w", err)
			}
			if err := s.store.SaveNamespaceState(s.wsID, s.nsID, string(state.Status), string(raw)); err != nil {
				return fmt.Errorf("save incomplete snapshot state: %w", err)
			}
			return nil
		},
		Commit: func() error {
			pins := make(map[deps.ID]deps.DependencyState)
			if s.infer != nil {
				maps.Copy(pins, snapshot.DependencyStatesForVolumes(meta.Volumes, s.infer(meta)))
			}
			maps.Copy(pins, snapshot.DependencyStatesForVolumes(meta.Volumes, meta.Dependencies))
			if s.runtime != nil {
				return s.runtime.CommitSnapshotDependencies(pins, commit)
			}
			state, err := s.readState()
			if err != nil {
				return err
			}
			namespace.ApplySnapshotPins(state, pins)
			state.Status = namespace.NsStatusStopped
			state.SnapshotImportPending = false
			return commit(state)
		},
	}, nil
}
