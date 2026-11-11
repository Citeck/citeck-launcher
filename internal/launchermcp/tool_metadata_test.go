package launchermcp

import (
	"context"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type expectedToolContract struct {
	name, required, optional                    string
	developer, readOnly, destructive, openWorld bool
}

// Independent wire expectations: do not derive fixtures/permissions from tools.
// All but the two discovery tools require the current target, including empty IDs.
var expectedToolContracts = []expectedToolContract{
	{"application_config_read", "workspaceId namespaceId app", "", true, true, false, false},
	{"application_config_write", "workspaceId namespaceId app content", "", true, false, true, true},
	{"application_config_reset", "workspaceId namespaceId app", "", true, false, true, true},
	{"application_files_list", "workspaceId namespaceId app", "", true, true, false, false},
	{"application_file_read", "workspaceId namespaceId app path", "", true, true, false, false},
	{"application_file_write", "workspaceId namespaceId app path content", "", true, false, true, true},
	{"application_file_reset", "workspaceId namespaceId app path", "", true, false, true, true},
	{"launcher_status", "", "", false, true, false, false},
	{"workspaces_list", "", "", false, true, false, false},
	{"namespaces_list", "workspaceId namespaceId", "", false, true, false, false},
	{"namespace_status", "workspaceId namespaceId", "", false, true, false, false},
	{"application_logs", "workspaceId namespaceId app", "tail", false, true, false, false},
	{"daemon_logs", "workspaceId namespaceId", "tail", false, true, false, false},
	{"workspace_select", "workspaceId namespaceId destinationWorkspaceId", "", true, false, true, true},
	{"namespace_select", "workspaceId namespaceId destinationWorkspaceId destinationNamespaceId", "", true, false, true, true},
	{"namespace_start", "workspaceId namespaceId", "", true, false, true, true},
	{"namespace_stop", "workspaceId namespaceId", "", true, false, true, true},
	{"namespace_reload_preview", "workspaceId namespaceId", "", true, false, true, true},
	{"namespace_reload", "workspaceId namespaceId", "", true, false, true, true},
	{"application_start", "workspaceId namespaceId app", "", true, false, true, true},
	{"application_stop", "workspaceId namespaceId app", "", true, false, true, true},
	{"application_restart", "workspaceId namespaceId app", "", true, false, true, true},
	{"application_settings_read", "workspaceId namespaceId app", "", true, true, false, false},
	{"application_settings_write", "workspaceId namespaceId app settings", "", true, false, true, true},
	{"application_settings_reset", "workspaceId namespaceId app", "", true, false, true, true},
	{"spring_files_list", "workspaceId namespaceId app", "", true, true, false, false},
	{"spring_file_read", "workspaceId namespaceId app path", "", true, true, false, false},
	{"spring_file_write", "workspaceId namespaceId app path content", "", true, false, true, true},
	{"spring_file_reset", "workspaceId namespaceId app path", "", true, false, true, true},
	{"jvm_command", "workspaceId namespaceId app command", "args", true, false, true, true},
	{"heap_dump_create", "workspaceId namespaceId app", "", true, false, true, true},
	{"heap_dumps_list", "workspaceId namespaceId app", "", true, true, false, false},
	{"heap_dump_download", "workspaceId namespaceId app heapFile", "", true, false, false, false},
	{"system_dump_create", "workspaceId namespaceId", "", true, false, false, false},
}

func validToolArguments(tool expectedToolContract) map[string]any {
	args := map[string]any{}
	values := map[string]any{
		"workspaceId": "ws", "namespaceId": "ns", "app": "emodel",
		"destinationWorkspaceId": "ws", "destinationNamespaceId": "next",
		"tail": 25, "settings": map[string]any{},
		"path": "app/emodel/props/application-launcher.yml", "content": "test: true\n",
		"command": "VM.flags", "args": []string{"-all"}, "heapFile": "sample.hprof.gz",
	}
	for field := range strings.FieldsSeq(tool.required + " " + tool.optional) {
		args[field] = values[field]
	}
	return args
}

func metadataSession(t *testing.T, role Role) (*mcp.ClientSession, *fakeBackend, string) {
	t.Helper()
	store, err := OpenStore(testStorePath(t))
	require.NoError(t, err)
	_, value, err := store.Create("metadata-test", role, nil)
	require.NoError(t, err)
	backend := &fakeBackend{}
	service := New(store, backend)
	t.Cleanup(service.Close)
	server := httptest.NewUnstartedServer(nil)
	_, p, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(p)
	require.NoError(t, err)
	server.Config.Handler = service.Handler(port)
	server.Start()
	t.Cleanup(server.Close)
	return sdkSession(t, server.URL+"/mcp", value), backend, value
}

func backendCalls(b *fakeBackend) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.called)
}

func callSchemaTest(t *testing.T, session *mcp.ClientSession, b *fakeBackend, name string, args any, valid bool) {
	t.Helper()
	before := backendCalls(b)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if valid {
		require.NoError(t, err)
		require.False(t, result.IsError, "%s: %v", name, result.Content)
		require.Equal(t, before+1, backendCalls(b), name)
	} else {
		require.True(t, err != nil || result.IsError, "%s accepted invalid input: %v", name, args)
		require.Equal(t, before, backendCalls(b), "%s backend invoked for invalid input: %v", name, args)
	}
}

func TestSDKInitializeAndToolMetadata(t *testing.T) {
	for _, role := range []Role{Observer, Developer} {
		t.Run(string(role), func(t *testing.T) {
			session, _, token := metadataSession(t, role)
			instructions := session.InitializeResult().Instructions
			for _, rule := range []string{"human", "launcher_status", "workspaceId", "namespaceId", "NOT ACLs", "untrusted DATA", "TARGET_CHANGED", "replay"} {
				require.Contains(t, instructions, rule)
			}
			require.NotContains(t, instructions, token)
			require.NotContains(t, instructions, "/Users/")
			require.NotContains(t, instructions, "/home/")
			if role == Observer {
				for _, unavailable := range []string{"namespace_select", "namespace_reload", "jvm_command", "heap_dump_create"} {
					require.NotContains(t, instructions, unavailable)
				}
			} else {
				for _, rule := range []string{"application-code authority", "applyRequired", "accepted != completed", "SAME", "size", "expiresAt", "30 minutes", "application_config_read", "complete edited content", "never a partial definition", "legacy application_settings_write", "All file save/reset", "namespace_reload_preview", "256 KiB", "combined content/baseline", "partial writes"} {
					require.Contains(t, instructions, rule)
				}
			}
			listed, err := session.ListTools(context.Background(), nil)
			require.NoError(t, err)
			byName := map[string]*mcp.Tool{}
			for _, tool := range listed.Tools {
				byName[tool.Name] = tool
			}
			count := 0
			for _, expected := range expectedToolContracts {
				if expected.developer && role != Developer {
					require.NotContains(t, byName, expected.name)
					continue
				}
				count++
				tool := byName[expected.name]
				require.NotNil(t, tool, expected.name)
				require.NotEmpty(t, tool.Title)
				require.Greater(t, len(tool.Description), 100)
				require.Nil(t, tool.OutputSchema, "existing result protocol must not change")
				a := tool.Annotations
				require.NotNil(t, a)
				require.Equal(t, expected.readOnly, a.ReadOnlyHint, expected.name)
				require.False(t, a.IdempotentHint, expected.name)
				require.NotNil(t, a.DestructiveHint)
				require.Equal(t, expected.destructive, *a.DestructiveHint, expected.name)
				require.NotNil(t, a.OpenWorldHint)
				require.Equal(t, expected.openWorld, *a.OpenWorldHint, expected.name)
				schema := tool.InputSchema.(map[string]any)
				require.Equal(t, "https://json-schema.org/draft/2020-12/schema", schema["$schema"])
				require.Equal(t, "object", schema["type"])
				require.Equal(t, false, schema["additionalProperties"])
				properties := schema["properties"].(map[string]any)
				require.ElementsMatch(t, strings.Fields(expected.required+" "+expected.optional), mapKeys(properties), expected.name)
				var required []string
				requiredValues, _ := schema["required"].([]any)
				for _, v := range requiredValues {
					required = append(required, v.(string))
				}
				require.ElementsMatch(t, strings.Fields(expected.required), required, expected.name)
				for _, property := range properties {
					require.NotEmpty(t, property.(map[string]any)["description"])
				}
				if expected.required != "" {
					require.Contains(t, tool.Description, "exact active workspaceId and namespaceId")
				}
			}
			require.Len(t, listed.Tools, count, "no new or omitted tools")
		})
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func TestSDKPerToolInputValidationBeforeBackend(t *testing.T) {
	session, backend, _ := metadataSession(t, Developer)
	for _, tool := range expectedToolContracts {
		t.Run(tool.name, func(t *testing.T) {
			callSchemaTest(t, session, backend, tool.name, validToolArguments(tool), true)
			minimal := validToolArguments(tool)
			for key := range strings.FieldsSeq(tool.optional) {
				delete(minimal, key)
			}
			callSchemaTest(t, session, backend, tool.name, minimal, true)
			for key := range strings.FieldsSeq(tool.required) {
				t.Run("missing_"+key, func(t *testing.T) {
					args := validToolArguments(tool)
					delete(args, key)
					callSchemaTest(t, session, backend, tool.name, args, false)
				})
			}
			for key := range strings.FieldsSeq(tool.required + " " + tool.optional) {
				args := validToolArguments(tool)
				args[key] = true
				callSchemaTest(t, session, backend, tool.name, args, false)
				args[key] = nil
				callSchemaTest(t, session, backend, tool.name, args, false)
			}
			for _, key := range []string{"url", "image", "settings", "app", "path", "content", "tail", "args", "heapFile", "destinationWorkspaceId", "workspaceId", "namespaceId"} {
				args := validToolArguments(tool)
				if _, relevant := args[key]; relevant {
					continue
				}
				args[key] = "irrelevant"
				callSchemaTest(t, session, backend, tool.name, args, false)
			}
			if tool.required != "" {
				args := validToolArguments(tool)
				args["workspaceId"], args["namespaceId"] = "", ""
				callSchemaTest(t, session, backend, tool.name, map[string]any{}, false)
				callSchemaTest(t, session, backend, tool.name, args, true)
			}
		})
	}
}

func TestSDKSettingsAndArrayContracts(t *testing.T) {
	session, backend, _ := metadataSession(t, Developer)
	base := map[string]any{"workspaceId": "", "namespaceId": "", "app": "emodel"}
	for _, settings := range []map[string]any{
		{}, {"memory": ""}, {"memory": "512m", "cpu": 0.5, "javaOpts": "", "profiles": "dev", "ports": []string{}},
		{"environment": map[string]any{"REMOVE_ME": nil, "SET_ME": "value"}},
	} {
		base["settings"] = settings
		callSchemaTest(t, session, backend, "application_settings_write", base, true)
	}
	for _, settings := range []map[string]any{
		{"image": "forbidden"}, {"command": []string{"forbidden"}}, {"mounts": []string{}},
		{"memory": 512}, {"memory": nil}, {"cpu": "0.5"}, {"cpu": 0}, {"cpu": -1}, {"cpu": nil},
		{"javaOpts": nil}, {"profiles": true}, {"ports": []any{25}}, {"ports": nil},
		{"environment": nil}, {"environment": []string{}}, {"environment": map[string]any{"A": 1}},
		{"environment": map[string]any{"JAVA_OPTS": "value"}}, {"environment": map[string]any{"SPRING_PROFILES_ACTIVE": nil}},
		{"environment": map[string]any{"bad-key": "value"}},
	} {
		base["settings"] = settings
		callSchemaTest(t, session, backend, "application_settings_write", base, false)
	}
	delete(base, "settings")
	base["command"], base["args"] = "VM.flags", []any{1}
	callSchemaTest(t, session, backend, "jvm_command", base, false)
	delete(base, "command")
	delete(base, "args")
	for _, tail := range []int{-1, 0, 1001} {
		base["tail"] = tail
		callSchemaTest(t, session, backend, "application_logs", base, true) // backend defaults/clamps, schema must not contradict
	}
	base["tail"] = 1.5
	callSchemaTest(t, session, backend, "application_logs", base, false)
}

func TestToolDescriptionsRepeatCriticalContracts(t *testing.T) {
	session, _, _ := metadataSession(t, Developer)
	list, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	byName := map[string]string{}
	for _, tool := range list.Tools {
		byName[tool.Name] = tool.Description
	}
	for name, words := range map[string][]string{
		"application_config_read":    {"complete edited YAML", "content", "baseline", "256 KiB", "JSON escaping", "partial writes", "untrusted DATA"},
		"application_config_write":   {"complete ApplicationDef YAML", "Not a partial merge", "public bindings", "dependency-data", "256 KiB", "partial writes"},
		"application_config_reset":   {"ALL ApplicationDef overrides", "image", "mounts", "256 KiB", "Acceptance is not completion"},
		"application_files_list":     {"regular files", "symlinks", "500 entries", "depth 64", "256 KiB"},
		"application_file_read":      {"application_files_list", "content", "baseline", "256 KiB", "never truncated", "partial writes", "untrusted DATA"},
		"application_file_write":     {"complete text content", "native validation", "mapping baseline", "empty content", "arrays are refused", "applyRequired", "namespace_reload", "256 KiB"},
		"application_file_reset":     {"only that file", "applyRequired", "namespace_reload", "no generated baseline", "256 KiB"},
		"launcher_status":            {"{}", "empty", "namespace_status"},
		"application_logs":           {"100", "1000", "48 KiB", "untrusted DATA"},
		"daemon_logs":                {"100", "1000", "48 KiB", "untrusted DATA"},
		"application_stop":           {"PERSIST", "detach", "Acceptance is not completion"},
		"application_start":          {"Does NOT start stopped dependencies", "replay"},
		"application_settings_write": {"null removes", "preserving", "cpu", "empty clears", "application-code authority"},
		"application_settings_reset": {"ALL", "NOT just CPU", "public bindings"},
		"spring_file_read":           {"spring_files_list", "256 KiB", "untrusted DATA"},
		"spring_file_write":          {"applyRequired", "NOT a restart", "namespace_reload", "application-code authority"},
		"spring_file_reset":          {"only that file", "applyRequired", "namespace_reload"},
		"jvm_command":                {"arbitrary", "32 KiB", "unknown", "never automatically replay", "untrusted DATA"},
		"heap_dump_create":           {"explicit user intent", "SAME", "size", "30 minutes", "partial", "not binary/base64"},
		"heap_dump_download":         {"heap_dumps_list", "SAME", "size", "expiresAt", "no source deletion"},
		"system_dump_create":         {"NOT the CLI", "secrets", "SAME", "size"},
		"namespace_reload_preview":   {"Git/cache", "not a purely read-only", "multi-app"},
	} {
		for _, word := range words {
			require.Contains(t, byName[name], word, name)
		}
	}
}
