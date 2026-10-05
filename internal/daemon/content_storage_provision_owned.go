package daemon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/citeck/citeck-launcher/internal/msg"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// The two ECOS records the launcher OWNS: the secret and the endpoint of its
// S3 storage. On every run whose default is ours or the built-in LOCAL one —
// connected, switched back to the database (our storage still serves every
// file written to it while it was the default), or about to be connected —
// each of them that exists is brought up to the namespace's current values;
// an administrator's edit of either is overwritten — except while the
// provider runs with a key that is not the namespace's (an operator replaced
// it with `citeck edit`): then the launcher no longer knows the key the
// provider checks, the secret is left alone and the endpoint keeps the
// credentials it names, and only the endpoint's URL is still put right. When
// an administrator pointed the default at a storage of their own, ECOS is
// theirs and nothing is written, these two included. The storage record and
// the default are never rewritten here.
//
// WHY the secret drifts: the key lives in two places — the namespace's secret
// value, which the provider's container is started with, and ECOS's database,
// which ecos-content reads it from. Anything that gives the namespace a NEW
// key while keeping the database (a snapshot restored into a new namespace, a
// desktop secrets reset; both regenerate a generated key) leaves ECOS on the
// old one, and every upload is refused with 403 under a banner that says
// "connected".
//
// HOW it is detected: ECOS never returns a secret's data — no attribute, no
// query predicate, no fingerprint — so the launcher remembers its own last
// write (NsPersistedState.ContentStorageSecretWrite: a salted hash of the key
// and the record's `modified` as ECOS answered right after). The secret is
// written again when that record is missing (a new namespace, a restore), its
// hash does not match the current key (a reset), or ECOS's `modified` moved
// (an edit, a database from another stand); and when ECOS does not say when
// it changed, on every run. A secret that is current is not written at all:
// every save of it re-encrypts the data, moves `modified` and makes every
// ecos-content instance drop and rebuild its S3 clients.
//
// WHY ecos-content needs no restart: emodel's secret service emits
// "secret-changed" after every save; ecos-content listens to it on every
// instance, maps the secret to the endpoint that names it, and
// S3ContentStorageFactory closes the storage built on that endpoint, which the
// next request rebuilds from the CURRENT endpoint and secret (ecos-content
// 1.7.0 / ecos-webapp-lib 3.33.0). An endpoint write emits "endpoint-changed"
// with the same effect. The one gap is the transport: the queue is not
// durable, so an ecos-content that misses the event (RabbitMQ or ZooKeeper
// down at that moment) keeps its old client until `citeck restart content`.
// Restarting it after every write instead would cost minutes of content
// downtime on exactly the starts that need the write, for a case the event
// already covers.

const (
	// csKeyHashScheme prefixes ContentStorageSecretWrite.KeyHash.
	csKeyHashScheme = "sha256"
	csKeyHashSalt   = 16
)

// csOwnPlan is what the launcher's own records need: why each is written
// again, "" for one that is left as it is.
type csOwnPlan struct {
	secret   string
	endpoint string
	// keepEndpointCredentials: the endpoint names credentials other than the
	// launcher's secret and the provider does not run with the namespace's
	// key, so an endpoint write sets its URL alone (see planOwnRecords).
	keepEndpointCredentials bool
}

func (pl csOwnPlan) empty() bool { return pl.secret == "" && pl.endpoint == "" }

// csAdminStorageIsDefault reports whether the default content storage is one
// an administrator chose: neither ours nor the built-in LOCAL one. ECOS is
// theirs then, and the launcher writes nothing at all.
func csAdminStorageIsDefault(snap csSnapshot, ids csRecordIDs) bool {
	return snap.defaultRef != "" && snap.defaultRef != csLocalStorageRef && snap.defaultRef != ids.storage
}

// keepOwnRecords brings the launcher's existing secret and endpoint up to the
// namespace's current values on a run that reached a verdict. ended is true
// when the run is over without one — canceled, or the platform went away
// (already said) — and step is the write or read that failed otherwise.
func (p *contentStorageProvisioner) keepOwnRecords(ctx context.Context, client ecosRecords, j *csJob, snap csSnapshot, ids csRecordIDs, creds csCredentials) (step *csStepError, ended bool) {
	if csAdminStorageIsDefault(snap, ids) {
		return nil, false
	}
	plan, err := p.planOwnRecords(ctx, client, j, snap, ids, creds)
	if err != nil {
		return csReadStep(err, creds), false
	}
	if plan.empty() {
		return nil, false
	}
	if !p.mayWrite(ctx, j) {
		return nil, true
	}
	return p.writeOwnRecords(ctx, client, j, ids, plan, creds), false
}

// planOwnRecords reads what ECOS says about the launcher's records that exist
// and decides which of them have to be written again.
func (p *contentStorageProvisioner) planOwnRecords(ctx context.Context, client ecosRecords, j *csJob, snap csSnapshot, ids csRecordIDs, creds csCredentials) (csOwnPlan, error) {
	var refs []string
	if snap.secretExists {
		refs = append(refs, ids.secret)
	}
	if snap.endpointExists {
		refs = append(refs, ids.endpoint)
	}
	if len(refs) == 0 {
		return csOwnPlan{}, nil
	}
	// `modified` is the secret's (a bean that does not answer `_modified`);
	// `url` and `credentials` are the endpoint's. Each record leaves the
	// others' attributes unanswered.
	recs, err := client.Query(ctx, refs, map[string]string{
		"modified":    "modified?str",
		"url":         "url?str",
		"credentials": "credentials?localId",
	})
	if err != nil {
		return csOwnPlan{}, fmt.Errorf("read the S3 secret and endpoint: %w", err)
	}
	// Whether the launcher still knows the key the provider checks. When it
	// does not — an operator replaced it on this stand — neither of the two
	// writes that would put the namespace's key in front of ecos-content may
	// happen: the secret is not rewritten with it, and the endpoint is not
	// pointed back at the launcher's secret, which holds it. Either one is
	// the very 403 the sync exists to prevent.
	runsWithKey := csProviderRunsWithKey(j.in.runtime, ids.provider, creds.secretKey)
	var plan csOwnPlan
	for i, ref := range refs {
		switch ref {
		case ids.secret:
			plan.secret = csSecretStale(j.in.runtime.ContentStorageSecretWrite(), ids.secret, creds,
				jsonString(recs[i]["modified"]))
			if plan.secret != "" && !runsWithKey {
				slog.Warn("The S3 key the provider runs with is not this namespace's key (changed with "+
					"`citeck edit`?): the ECOS secret is left as it is", "ns", j.in.nsID, "provider", ids.provider,
					"record", ids.secret)
				plan.secret = ""
			}
		case ids.endpoint:
			url, credentials := jsonString(recs[i]["url"]), jsonString(recs[i]["credentials"])
			wantCredentials := csSecretLocalID(ids.provider)
			if credentials != wantCredentials && !runsWithKey {
				// The operator who rotated the key by hand may have put it
				// into a secret of their own and pointed the endpoint at it.
				// The URL is still the launcher's to keep; the credentials
				// are judged as they are.
				slog.Warn("The S3 key the provider runs with is not this namespace's key (changed with "+
					"`citeck edit`?): the ECOS endpoint keeps the credentials it names", "ns", j.in.nsID,
					"provider", ids.provider, "record", ids.endpoint, "credentials", credentials)
				plan.keepEndpointCredentials = true
				wantCredentials = credentials
			}
			plan.endpoint = csEndpointStale(url, credentials, j.in.s3.URL, wantCredentials)
		}
	}
	return plan, nil
}

// writeOwnRecords writes the records the plan names. Each write is logged
// with its reason, never with the key.
func (p *contentStorageProvisioner) writeOwnRecords(ctx context.Context, client ecosRecords, j *csJob, ids csRecordIDs, plan csOwnPlan, creds csCredentials) *csStepError {
	if plan.secret != "" {
		// The full ref UPDATES and never creates: a secret deleted since the
		// read is refused, and the next pass sees it missing. Both data fields
		// are sent — ECOS keeps the old data when the new one is empty.
		_, err := client.Mutate(ctx, ids.secret, map[string]any{
			"type":      "BASIC",
			"data?json": map[string]any{"username": creds.accessKey, "password": creds.secretKey},
		})
		if err != nil {
			return stepFailed(err, msg.New("contentStorage.msg.provision.updateFailed",
				"record", ids.secret, "error", creds.redact(err.Error())))
		}
		slog.Info("Updated the ECOS S3 secret to the namespace's current key", "ns", j.in.nsID,
			"record", ids.secret, "reason", plan.secret)
		p.rememberSecretWrite(ctx, client, j, ids, creds)
	}
	if plan.endpoint != "" {
		atts := map[string]any{"url": j.in.s3.URL}
		if !plan.keepEndpointCredentials {
			atts["credentials"] = ids.secret
		}
		_, err := client.Mutate(ctx, ids.endpoint, atts)
		if err != nil {
			return stepFailed(err, msg.New("contentStorage.msg.provision.updateFailed",
				"record", ids.endpoint, "error", creds.redact(err.Error())))
		}
		slog.Info("Updated the ECOS S3 endpoint to the namespace's provider", "ns", j.in.nsID,
			"record", ids.endpoint, "reason", plan.endpoint)
	}
	return nil
}

// rememberSecretWrite records a write of the S3 credentials into the secret,
// with the `modified` ECOS answers for it now. A read-back that fails records
// no time: the next run then writes the secret once more, which is harmless.
func (p *contentStorageProvisioner) rememberSecretWrite(ctx context.Context, client ecosRecords, j *csJob, ids csRecordIDs, creds csCredentials) {
	modified := ""
	if recs, err := client.Query(ctx, []string{ids.secret}, map[string]string{"modified": "modified?str"}); err == nil {
		modified = jsonString(recs[0]["modified"])
	} else {
		slog.Debug("Could not read back when the ECOS S3 secret changed", "ns", j.in.nsID, "err", creds.redact(err.Error()))
	}
	hash, err := csKeyHash(creds)
	if err != nil {
		slog.Warn("Could not record the S3 secret written into ECOS; the next run writes it again",
			"ns", j.in.nsID, "err", err)
		return
	}
	p.recordSecretWrite(ctx, j, &namespace.ContentStorageSecretWrite{Secret: ids.secret, KeyHash: hash, Modified: modified})
}

// recordSecretWrite persists the record of a secret write, under p.mu with
// the job check for the reason recordSwitch is: a run that lost the race with
// a namespace switch or delete must not write a state row back.
func (p *contentStorageProvisioner) recordSecretWrite(ctx context.Context, j *csJob, w *namespace.ContentStorageSecretWrite) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if j != p.job || ctx.Err() != nil {
		return
	}
	_ = j.in.runtime.SetContentStorageSecretWrite(w)
}

// csSecretStale answers why the launcher's secret has to be written again, ""
// when it holds the namespace's current key as far as anyone can tell.
func csSecretStale(last *namespace.ContentStorageSecretWrite, secretRef string, creds csCredentials, modified string) string {
	switch {
	case last == nil || last.Secret != secretRef:
		return "no record of the launcher writing it"
	case !csKeyHashMatches(last.KeyHash, creds):
		return "the namespace's S3 key changed"
	case modified == "":
		return "ECOS does not say when it last changed"
	case modified != last.Modified:
		return "it changed in ECOS after the launcher wrote it"
	default:
		return ""
	}
}

// csEndpointStale answers why the launcher's endpoint has to be written
// again, "" when it names the provider's URL and the launcher's secret. The
// secret is compared by local id: that is all ecos-model reads from it.
func csEndpointStale(url, credentialsLocalID, wantURL, wantSecretLocalID string) string {
	var why []string
	if url != wantURL {
		why = append(why, "its URL differs")
	}
	if credentialsLocalID != wantSecretLocalID {
		why = append(why, "it names other credentials")
	}
	return strings.Join(why, ", ")
}

// csProviderRunsWithKey reports whether the provider's running container was
// started with this secret key — the key appears in its environment or its
// command line. The generation hands the provider the namespace's key through
// `${secret:}`, so it is there unless an operator replaced it on this stand
// (`citeck edit <app>`). Then the launcher no longer knows the key the
// provider checks, an operator who rotated it by hand has put it into ECOS by
// hand as well — into the launcher's secret or into one of their own that the
// endpoint names — and writing the namespace's key over it, or pointing the
// endpoint back at the secret that holds it, would cause exactly the 403 the
// sync exists to prevent.
func csProviderRunsWithKey(rt *namespace.Runtime, provider, key string) bool {
	app := rt.FindApp(provider)
	if key == "" || app == nil {
		return false
	}
	for _, e := range app.Def.Environments {
		if strings.Contains(e.Value, key) {
			return true
		}
	}
	for _, arg := range app.Def.Cmd {
		if strings.Contains(arg, key) {
			return true
		}
	}
	return false
}

// csKeyHash is a salted digest of the credentials: "sha256:<salt>:<digest>".
func csKeyHash(creds csCredentials) (string, error) {
	salt := make([]byte, csKeyHashSalt)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate a salt: %w", err)
	}
	return csKeyHashScheme + ":" + hex.EncodeToString(salt) + ":" + hex.EncodeToString(csKeyDigest(salt, creds)), nil
}

// csKeyHashMatches reports whether a stored digest is of these credentials.
// Anything it cannot read does not match.
func csKeyHashMatches(stored string, creds csCredentials) bool {
	scheme, rest, ok := strings.Cut(stored, ":")
	if !ok || scheme != csKeyHashScheme {
		return false
	}
	saltHex, digestHex, ok := strings.Cut(rest, ":")
	if !ok {
		return false
	}
	salt, err1 := hex.DecodeString(saltHex)
	digest, err2 := hex.DecodeString(digestHex)
	if err1 != nil || err2 != nil || len(salt) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(digest, csKeyDigest(salt, creds)) == 1
}

// csKeyDigest is SHA-256 over salt, access key, NUL, secret key.
func csKeyDigest(salt []byte, creds csCredentials) []byte {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(creds.accessKey))
	h.Write([]byte{0})
	h.Write([]byte(creds.secretKey))
	return h.Sum(nil)
}

// csReadStep is a failed read of ECOS in the middle of a pass.
func csReadStep(err error, creds csCredentials) *csStepError {
	return &csStepError{
		msg:       msg.New("contentStorage.msg.provision.readFailed", "error", creds.redact(err.Error())),
		retryable: csRetryable(err),
	}
}

// jsonString reads a JSON string; "" for anything else, null included.
func jsonString(raw json.RawMessage) string {
	var s *string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil || s == nil {
		return ""
	}
	return *s
}
