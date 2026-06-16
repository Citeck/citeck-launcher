package appdef

import "testing"

// TestGetHashInput_Golden pins the EXACT GetHashInput output for a fully
// populated ApplicationDef. GetHashInput is a hard cross-version compatibility
// contract: the deployment hash computed from it is stored in container labels
// (LabelAppHash) and compared across launcher versions to decide whether a
// running container can be adopted or must be recreated. Any change to the
// fields included, their ordering, or their formatting silently busts every
// deployed hash and recreates all containers on upgrade — such changes require
// an explicit migration, so this test MUST fail loudly when they happen.
//
// Covered hash inputs: name, image, imageDigest, cmd, shmSize,
// volumesContentHash, environments (sorted by key), ports (sorted), volumes
// (declaration order), dependsOn (sorted by key), initActions, initContainers
// (image only), resources memory limit.
//
// Intentionally NOT part of the hash (also pinned here by populating them and
// asserting they leave no trace in the golden string): NetworkAliases,
// StartupConditions, LivenessProbe, Kind, IsInit, StopTimeout.
func TestGetHashInput_Golden(t *testing.T) {
	def := ApplicationDef{
		Name:           "emodel",
		NetworkAliases: []string{"emodel-alias"}, // not hashed
		Image:          "nexus.citeck.ru/ecos-model:2.5.0",
		ImageDigest:    "sha256:0123456789abcdef",
		Environments: map[string]string{
			// Deliberately out of order — GetHashInput must sort by key.
			"SERVER_PORT":            "17022",
			"ECOS_INIT_DELAY":        "0",
			"SPRING_PROFILES_ACTIVE": "dev,launcher",
		},
		Cmd:                []string{"start", "--http-enabled=true"},
		Ports:              []string{"8080:80", "17022:17022"}, // out of order — must sort
		Volumes:            []string{"emodel2:/data", "./app/emodel/props:/run/props"},
		VolumesContentHash: "vchAbc123",
		InitActions: []AppInitAction{
			{Exec: []string{"sh", "-c", "/init_db_and_user.sh emodel"}},
			{Exec: []string{"rabbitmqctl", "add_user", "citeck", "pass"}},
		},
		// Deliberately NOT in sorted order — GetHashInput sorts dependsOn, so the
		// insertion order a StringSet preserves on the wire must leave no trace
		// in the hash (the golden below lists them sorted).
		DependsOn: NewStringSet("zookeeper", "postgres", "rabbitmq"),
		StartupConditions: []StartupCondition{ // not hashed
			{Probe: &AppProbeDef{HTTP: &HTTPProbeDef{Path: "/management/health", Port: 17022}}},
		},
		LivenessProbe: &AppProbeDef{ // not hashed
			HTTP: &HTTPProbeDef{Path: "/management/health", Port: 17022},
		},
		Resources: &AppResourcesDef{Limits: LimitsDef{Memory: "1g"}},
		Kind:      KindCiteckCore, // not hashed
		ShmSize:   "128m",
		InitContainers: []InitContainerDef{
			{Image: "nexus.citeck.ru/ecos-apps-init:1.0.0", Cmd: []string{"cp", "-r", "/a", "/b"}},
			{Image: "citeck/launcher-utils:1.1.0"},
		},
		IsInit:      false, // not hashed
		StopTimeout: 30,    // not hashed
	}

	const golden = "name=emodel\n" +
		"image=nexus.citeck.ru/ecos-model:2.5.0\n" +
		"imageDigest=sha256:0123456789abcdef\n" +
		"cmd=start --http-enabled=true\n" +
		"shmSize=128m\n" +
		"vch=vchAbc123\n" +
		"env:ECOS_INIT_DELAY=0\n" +
		"env:SERVER_PORT=17022\n" +
		"env:SPRING_PROFILES_ACTIVE=dev,launcher\n" +
		"port=17022:17022\n" +
		"port=8080:80\n" +
		"vol=emodel2:/data\n" +
		"vol=./app/emodel/props:/run/props\n" +
		"dep=postgres\n" +
		"dep=rabbitmq\n" +
		"dep=zookeeper\n" +
		"initAction=sh -c /init_db_and_user.sh emodel\n" +
		"initAction=rabbitmqctl add_user citeck pass\n" +
		"initContainer=nexus.citeck.ru/ecos-apps-init:1.0.0\n" +
		"initContainer=citeck/launcher-utils:1.1.0\n" +
		"mem=1g\n"

	if got := def.GetHashInput(); got != golden {
		t.Fatalf("GetHashInput drifted from the pinned cross-version contract — "+
			"this busts deployed container hashes and requires a migration.\ngot:\n%s\nwant:\n%s", got, golden)
	}
}

// TestGetHash_VolumesContentHashParticipates guards against a regression
// where ApplicationDef.VolumesContentHash is silently dropped from
// GetHashInput — that would mean changes to bind-mounted file content
// stop triggering container recreates, and deployments would silently
// run with stale config.
func TestGetHash_VolumesContentHashParticipates(t *testing.T) {
	base := ApplicationDef{
		Name:  "x",
		Image: "img:1",
	}
	withHash := base
	withHash.VolumesContentHash = "abc123"

	if base.GetHash() == withHash.GetHash() {
		t.Fatal("VolumesContentHash does not affect the deployment hash — regression against the bind-mount content change detection")
	}
}
