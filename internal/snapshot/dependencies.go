package snapshot

import (
	"github.com/citeck/citeck-launcher/internal/deps"
)

// DependencyStatesForVolumes carries only pins whose active data is in the
// archive. A partial snapshot must not overwrite pins for untouched data.
// Retained rollback targets travel only if their data volume was exported too.
func DependencyStatesForVolumes(volumes []VolumeSnapshotMeta, states map[deps.ID]deps.DependencyState) map[deps.ID]deps.DependencyState {
	generations := make(map[deps.ID]map[int]bool)
	for _, v := range volumes {
		id, gen, ok := deps.ParseVolumeName(v.Name)
		if !ok {
			continue
		}
		if generations[id] == nil {
			generations[id] = make(map[int]bool)
		}
		generations[id][gen] = true
	}
	out := make(map[deps.ID]deps.DependencyState)
	for id, state := range states {
		if id == deps.Keycloak || state.Image == "" || !generations[id][state.Gen()] {
			continue
		}
		if previous, ok := state.Previous(); ok && !generations[id][previous.Gen()] {
			state.PrevImage = ""
			state.PrevVolumeGen = 0
		}
		out[id] = state
	}
	// Keycloak's schema/version belongs to PostgreSQL's imported data.
	if _, have := out[deps.Postgres]; have {
		if state, exists := states[deps.Keycloak]; exists && state.Image != "" {
			if previous, ok := state.Previous(); ok && !generations[deps.Postgres][previous.Gen()] {
				state = state.WithoutPrevious()
			}
			out[deps.Keycloak] = state
		}
	}
	return out
}
