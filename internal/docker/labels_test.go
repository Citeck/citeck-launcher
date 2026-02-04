package docker

import "testing"

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
