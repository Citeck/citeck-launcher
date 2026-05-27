package docker

import "testing"

func TestVolumeName(t *testing.T) {
	tests := []struct {
		name      string
		orig      string
		namespace string
		workspace string
		want      string
	}{
		{
			name:      "simple lowercase",
			orig:      "mongo2",
			namespace: "default",
			workspace: "main",
			want:      "citeck_volume_mongo2_default_main",
		},
		{
			name:      "namespace and workspace lowercased",
			orig:      "pgadmin2",
			namespace: "DEV",
			workspace: "TeamA",
			want:      "citeck_volume_pgadmin2_dev_teama",
		},
		{
			name:      "original name case preserved",
			orig:      "Mongo_Data",
			namespace: "ns",
			workspace: "ws",
			want:      "citeck_volume_Mongo_Data_ns_ws",
		},
		{
			name:      "empty workspace",
			orig:      "zookeeper2",
			namespace: "default",
			workspace: "",
			want:      "citeck_volume_zookeeper2_default_",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VolumeName(tt.orig, tt.namespace, tt.workspace)
			if got != tt.want {
				t.Errorf("VolumeName(%q,%q,%q) = %q; want %q", tt.orig, tt.namespace, tt.workspace, got, tt.want)
			}
		})
	}
}
