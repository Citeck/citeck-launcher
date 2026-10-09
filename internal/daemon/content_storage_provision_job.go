package daemon

import (
	"context"
	"log/slog"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/msg"
)

// run wires one immutable runner to scheduler-owned job checks and persistence.
func (p *contentStorageProvisioner) run(ctx context.Context, j *csJob) {
	defer p.finish(j)
	var creds csCredentials
	if def, ok := runtimeAppDef(j.in.runtime, "content"); ok {
		creds.secretKey, _ = def.Environments.Get("ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_PASSWORD")
	}
	gatewayAddr, records := p.gatewayAddr, p.records
	runner := csConnectionRunner{
		nsID: j.in.nsID, creds: creds, state: j.in.runtime,
		now: p.now, pollInterval: p.pollInterval, readyTimeout: p.readyTimeout,
		probe: func(ctx context.Context, ids csRecordIDs) (ecosRecords, csSnapshot, error) {
			addr, err := gatewayAddr(ctx, j.in.runtime)
			if err != nil {
				return nil, csSnapshot{}, err
			}
			client := records(addr)
			snap, err := csReadSnapshot(ctx, client, ids)
			return client, snap, err
		},
		mayWrite:      func(ctx context.Context) bool { return p.mayWrite(ctx, j) },
		recordSwitch:  func(ctx context.Context, ref string) bool { return p.recordSwitch(ctx, j, ref) },
		recordVerdict: func(ctx context.Context, connected bool) bool { return p.recordVerdict(ctx, j, connected) },
		provisioning:  func() { p.setState(j, api.ContentStorageStateProvisioning, msg.Message{}) },
		settle:        func(state, ref string) { p.settle(j, state, ref) },
		fail:          func(m msg.Message) { p.fail(j, m) },
		acquireMutation: func() (func(), bool) {
			release, ok := p.d.longOp.tryDataMutation()
			if !ok {
				p.abort(j)
			}
			return release, ok
		},
	}
	runner.run(ctx)
}

// mayWrite is the last look before a write into ECOS: nothing may be written
// for a namespace that is no longer active, or into a platform a snapshot is
// about to replace. A platform that went away postpones the run (abort).
func (p *contentStorageProvisioner) mayWrite(ctx context.Context, j *csJob) bool {
	if ctx.Err() != nil {
		return false
	}
	if !p.platformStillUp(j) {
		p.abort(j)
		return false
	}
	return true
}

// recordSwitch writes the pending-switch marker (a storage ref) or clears it
// (""), and answers false when the run is no longer the current one — then
// nothing is written, here or into ECOS. Held under p.mu with the job check
// on purpose: Reset (namespace switch, delete) takes p.mu BEFORE the runtime
// is shut down and its record deleted, so a run that lost the race cannot
// write a state row back for a namespace that is gone. A failed write is
// logged by the runtime and stays owed, but no remote mutation may proceed
// until the marker is durable; the provisioner reports the failure.
func (p *contentStorageProvisioner) recordSwitch(ctx context.Context, j *csJob, storageRef string) bool {
	p.mu.Lock()
	if j != p.job || ctx.Err() != nil {
		p.mu.Unlock()
		return false
	}
	err := j.in.runtime.SetContentStorageSwitch(storageRef)
	p.mu.Unlock()
	if err != nil {
		p.fail(j, msg.New("contentStorage.msg.provision.createFailed",
			"record", "namespace content-storage switch marker", "error", err.Error()))
		return false
	}
	return true
}

// recordVerdict persists switch history only after ECOS confirms native S3.
func (p *contentStorageProvisioner) recordVerdict(ctx context.Context, j *csJob, connected bool) bool {
	if !connected {
		return p.recordSwitch(ctx, j, "")
	}
	p.mu.Lock()
	if j != p.job || ctx.Err() != nil {
		p.mu.Unlock()
		return false
	}
	err := j.in.runtime.CompleteContentStorageSwitch()
	p.mu.Unlock()
	if err != nil {
		p.fail(j, msg.New("contentStorage.msg.provision.createFailed", "record", "namespace content-storage switch history", "error", err.Error()))
		return false
	}
	return true
}

// settle records a verdict from ECOS. Said in the log once per change, not on
// every re-verification.
func (p *contentStorageProvisioner) settle(j *csJob, state, defaultRef string) {
	if !p.setState(j, state, msg.Message{}) {
		return
	}
	if state == api.ContentStorageStateConnected {
		slog.Info("S3 content storage is connected", "ns", j.in.nsID, "storage", defaultRef)
		return
	}
	slog.Info("S3 content storage is managed by an administrator: the launcher leaves the default storage alone",
		"ns", j.in.nsID, "defaultStorage", defaultRef)
}
