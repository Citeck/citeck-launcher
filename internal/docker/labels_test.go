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

// TestContainerNameFormat verifies the container naming scheme.
func TestContainerNameFormat(t *testing.T) {
	c := &Client{namespace: "prod"}

	name := c.ContainerName("proxy")
	expected := "citeck_proxy_prod"
	if name != expected {
		t.Errorf("ContainerName() = %q, want %q", name, expected)
	}
}

// TestNetworkNameFormat verifies the network naming scheme.
func TestNetworkNameFormat(t *testing.T) {
	c := &Client{namespace: "prod"}

	name := c.NetworkName()
	expected := "citeck_network_prod"
	if name != expected {
		t.Errorf("NetworkName() = %q, want %q", name, expected)
	}
}
