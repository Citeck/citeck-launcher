package docker

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// TestDockerLabelsMatchKotlin verifies that Go labels match Kotlin DockerLabels.kt.
func TestDockerLabelsMatchKotlin(t *testing.T) {
	// From core/.../docker/DockerLabels.kt:
	//   const val APP_NAME = "citeck.launcher.app.name"
	//   const val APP_HASH = "citeck.launcher.app.hash"
	//   const val WORKSPACE = "citeck.launcher.workspace"
	//   const val NAMESPACE = "citeck.launcher.namespace"
	//   const val ORIGINAL_NAME = "citeck.launcher.original-name"
	//   const val LAUNCHER = "citeck.launcher"

	tests := []struct {
		goConst    string
		goValue    string
		kotlinName string
	}{
		{"LabelLauncher", LabelLauncher, "citeck.launcher"},
		{"LabelWorkspace", LabelWorkspace, "citeck.launcher.workspace"},
		{"LabelNamespace", LabelNamespace, "citeck.launcher.namespace"},
		{"LabelAppName", LabelAppName, "citeck.launcher.app.name"},
		{"LabelAppHash", LabelAppHash, "citeck.launcher.app.hash"},
		{"LabelOrigName", LabelOrigName, "citeck.launcher.original-name"},
		{"LabelComposeProj", LabelComposeProj, "com.docker.compose.project"},
	}

	for _, tt := range tests {
		if tt.goValue != tt.kotlinName {
			t.Errorf("%s = %q, want %q (must match Kotlin DockerLabels.kt)", tt.goConst, tt.goValue, tt.kotlinName)
		}
	}
}

// TestContainerNameFormat_Server verifies server mode naming (no workspace).
func TestContainerNameFormat_Server(t *testing.T) {
	c := &Client{namespace: "prod"}
	if got := c.ContainerName("proxy"); got != "citeck_proxy_prod" {
		t.Errorf("ContainerName() = %q, want %q", got, "citeck_proxy_prod")
	}
}

// TestContainerNameFormat_Desktop verifies desktop mode naming (with workspace, Kotlin compat).
func TestContainerNameFormat_Desktop(t *testing.T) {
	c := &Client{workspace: "default", namespace: "prod"}
	if got := c.ContainerName("proxy"); got != "citeck_proxy_prod_default" {
		t.Errorf("ContainerName() = %q, want %q", got, "citeck_proxy_prod_default")
	}
}

// TestNetworkNameFormat_Server verifies server mode network naming.
func TestNetworkNameFormat_Server(t *testing.T) {
	c := &Client{namespace: "prod"}
	if got := c.NetworkName(); got != "citeck_network_prod" {
		t.Errorf("NetworkName() = %q, want %q", got, "citeck_network_prod")
	}
}

// TestNetworkNameFormat_Desktop verifies desktop mode network naming (Kotlin compat).
func TestNetworkNameFormat_Desktop(t *testing.T) {
	c := &Client{workspace: "default", namespace: "prod"}
	if got := c.NetworkName(); got != "citeck_network_prod_default" {
		t.Errorf("NetworkName() = %q, want %q", got, "citeck_network_prod_default")
	}
}

// TestLabelWorkspaceValue verifies that the LabelWorkspace a container is
// created with carries the WORKSPACE ID (Kotlin contract — see the container
// labels list), never the namespace. The bug this pins is a real one the
// container-create path had: it wrote c.namespace into the workspace label
// while the network and utils-container paths wrote c.workspace, so a
// label-filter query answered differently depending on which object it looked
// at — and in server mode every container claimed to belong to a workspace
// named after its namespace.
//
// The assertions therefore read the label OUT of the producer
// (containerLabels, the only pure one — the network and volume writers need a
// live engine) instead of reading the field back off the struct the test just
// populated, which is what this test used to do and what could never have
// failed.
func TestLabelWorkspaceValue(t *testing.T) {
	app := appdef.ApplicationDef{Name: "postgres", Image: "postgres:17.5"}

	t.Run("server-mode-empty", func(t *testing.T) {
		c := &Client{workspace: "", namespace: "prod"}

		labels := c.containerLabels(app, app.Name, nil)

		if got, ok := labels[LabelWorkspace]; !ok || got != "" {
			t.Errorf("%s = %q (present=%v), want %q in server mode — never the namespace",
				LabelWorkspace, got, ok, "")
		}
		if labels[LabelWorkspace] == c.namespace {
			t.Errorf("%s must not be mis-attributed to the namespace %q", LabelWorkspace, c.namespace)
		}
		if labels[LabelNamespace] != "prod" {
			t.Errorf("%s = %q, want %q", LabelNamespace, labels[LabelNamespace], "prod")
		}
	})

	t.Run("desktop-mode-set", func(t *testing.T) {
		c := &Client{workspace: "default", namespace: "prod"}

		labels := c.containerLabels(app, app.Name, nil)

		if labels[LabelWorkspace] != "default" {
			t.Errorf("%s = %q, want %q", LabelWorkspace, labels[LabelWorkspace], "default")
		}
		if labels[LabelNamespace] != "prod" {
			t.Errorf("%s = %q, want %q", LabelNamespace, labels[LabelNamespace], "prod")
		}
	})
}
