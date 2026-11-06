package namespace

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileMergeUnavailableCurrentRefusesKeepCurrentFallback(t *testing.T) {
	textual, err := MakeFileEdit("run.sh", []byte("alpha\nbeta\ngamma\n"), []byte("alpha\nEDITED\ngamma\n"))
	require.NoError(t, err)
	structural, err := MakeFileEdit("application.yml", []byte("a: 1\n"), []byte("a: 2\n"))
	require.NoError(t, err)
	for _, c := range []struct {
		name     string
		edit     FileEdit
		template []byte
	}{{"run.sh", textual, []byte("completely unrelated replacement document")}, {"application.yml", structural, []byte("[invalid")}, {"unknown", FileEdit{Format: "future", Payload: json.RawMessage(`{}`)}, []byte("new template")}} {
		_, err = ApplyFileEdit(c.name, c.edit, c.template, nil)
		require.ErrorContains(t, err, "current is unavailable")
		got, applyErr := ApplyFileEdit(c.name, c.edit, c.template, []byte{})
		require.NoError(t, applyErr)
		require.Empty(t, got, "a legitimate empty current is not unavailable")
		files := map[string][]byte{c.name: c.template}
		require.Error(t, applyFileEditsToFiles(files, map[string]FileEdit{c.name: c.edit}, nil))
		require.Equal(t, c.template, files[c.name], "failed merge cannot silently select template")
	}
	got, err := ApplyFileEdit("application.yml", structural, []byte("a: 1\nb: 3\n"), nil)
	require.NoError(t, err)
	require.Contains(t, string(got), "a: 2")
	require.Contains(t, string(got), "b: 3")
	got, err = ApplyFileEdit("run.sh", textual, []byte("alpha\nbeta\ngamma\n"), nil)
	require.NoError(t, err)
	require.Contains(t, string(got), "EDITED")
}
func TestSpringMultiDocumentDeltaKeepsEveryDocument(t *testing.T) {
	base := []byte("a: 1\n---\nb: 2\n")
	edited := []byte("a: 9\n---\nb: 3\n")
	edit, err := MakeFileEdit("application.yml", base, edited)
	require.NoError(t, err)
	require.Equal(t, "textual", edit.Format)
	got, err := ApplyFileEdit("application.yml", edit, base, nil)
	require.NoError(t, err)
	require.Equal(t, edited, got)
}
func TestEditGeneratedFileCallbackAndResetIsolation(t *testing.T) {
	r := NewRuntime(testConfig(), newMockDocker(), t.TempDir())
	a := "app/gateway/props/application.yml"
	b := "app/gateway/props/application-other.yml"
	baseline := []byte("a: 1\n")
	r.SetLastGenFiles(map[string][]byte{a: baseline, b: baseline})
	r.SetStatePersister(failingPersister{})
	calls := 0
	write := func(content []byte) error { calls++; return nil }
	require.NoError(t, r.EditGeneratedFile(a, []byte("a: 2\n"), false, write))
	require.NoError(t, r.EditGeneratedFile(b, []byte("a: 3\n"), false, write))
	require.Equal(t, 2, calls)
	require.NotEmpty(t, r.StateWriteError())
	require.True(t, r.IsFileEdited(a))
	require.True(t, r.IsFileEdited(b))
	require.NoError(t, r.EditGeneratedFile(a, nil, true, func(content []byte) error { calls++; require.Equal(t, baseline, content); return nil }))
	require.Equal(t, 3, calls)
	require.False(t, r.IsFileEdited(a))
	require.True(t, r.IsFileEdited(b))
	require.Error(t, r.EditGeneratedFile(b, []byte("a: 4\n"), false, func([]byte) error { return context.Canceled }))
	require.True(t, r.IsFileEdited(b))
	require.Equal(t, 3, calls)
}
