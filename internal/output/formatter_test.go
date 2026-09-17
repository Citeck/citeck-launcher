package output

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
)

func TestFormatTable_BasicRendering(t *testing.T) {
	headers := []string{"APP", "STATUS", "IMAGE"}
	rows := [][]string{
		{"proxy", "RUNNING", "ecos-proxy:2.25"},
		{"gateway", "RUNNING", "ecos-gateway:3.3.0"},
	}

	result := FormatTable(headers, rows)

	if !strings.Contains(result, "APP") {
		t.Error("expected header APP in output")
	}
	if !strings.Contains(result, "proxy") {
		t.Error("expected proxy in output")
	}
	if !strings.Contains(result, "ecos-gateway:3.3.0") {
		t.Error("expected full image name in output")
	}
}

func TestFormatTable_ColumnWidths(t *testing.T) {
	headers := []string{"A", "B"}
	rows := [][]string{
		{"short", "x"},
		{"a-much-longer-value", "y"},
	}

	result := FormatTable(headers, rows)
	lines := strings.Split(result, "\n")

	// All lines should have same column alignment
	if len(lines) < 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	// Header column "A" should be padded to width of "a-much-longer-value"
	headerLine := lines[0]
	if !strings.HasPrefix(headerLine, "A") {
		t.Error("header should start with A")
	}
}

func TestFormatTable_EmptyHeaders(t *testing.T) {
	result := FormatTable(nil, nil)
	if result != "" {
		t.Error("expected empty string for nil headers")
	}
}

func TestFormatTable_EmptyRows(t *testing.T) {
	headers := []string{"APP", "STATUS"}
	result := FormatTable(headers, nil)
	if !strings.Contains(result, "APP") {
		t.Error("expected headers even with no rows")
	}
}

func TestFormatKeyValue(t *testing.T) {
	pairs := [][2]string{
		{"Name", "production"},
		{"Status", "RUNNING"},
	}
	result := FormatKeyValue(pairs)
	if !strings.Contains(result, "Name:") {
		t.Error("expected Name: in output")
	}
	if !strings.Contains(result, "production") {
		t.Error("expected production in output")
	}
}

func TestPrintJSON_ValidJSON(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintJSON(map[string]string{"key": "value"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify it's valid JSON
	var parsed map[string]string
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, output)
	}
	if parsed["key"] != "value" {
		t.Errorf("expected key=value, got key=%s", parsed["key"])
	}
}

func TestPrintJSON_NoANSI(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	SetFormat(FormatJSON)
	PrintJSON(map[string]string{"status": "RUNNING"})
	SetFormat(FormatText) // restore

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if strings.Contains(output, "\033[") {
		t.Error("JSON output should not contain ANSI escape codes")
	}
}

func TestFormatJSON_EmptyData(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintJSON(map[string]any{})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := strings.TrimSpace(buf.String())

	if output != "{}" {
		t.Errorf("expected {}, got %s", output)
	}
}

func TestVisibleLen_PlainText(t *testing.T) {
	if got := visibleLen("hello"); got != 5 {
		t.Errorf("visibleLen(hello) = %d, want 5", got)
	}
}

func TestVisibleLen_WithANSI(t *testing.T) {
	colored := "\033[32mRUNNING\033[0m"
	if got := visibleLen(colored); got != 7 {
		t.Errorf("visibleLen(colored RUNNING) = %d, want 7", got)
	}
}

func TestVisibleLen_BoldColor(t *testing.T) {
	bold := "\033[1m\033[31mFAILED\033[0m"
	if got := visibleLen(bold); got != 6 {
		t.Errorf("visibleLen(bold FAILED) = %d, want 6", got)
	}
}

func TestFormatTable_ANSIAlignment(t *testing.T) {
	prevColors := colorsEnabled
	SetColorsEnabled(true)
	defer SetColorsEnabled(prevColors)

	headers := []string{"APP", "STATUS", "IMAGE"}
	rows := [][]string{
		{"proxy", Colorize(Green, "RUNNING"), "img:1"},
		{"gateway", Colorize(Red, "FAILED"), "img:2"},
	}

	result := FormatTable(headers, rows)
	lines := strings.Split(result, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	// Verify IMAGE column starts at the same visible position in both data rows.
	stripped1 := ansiRE.ReplaceAllString(lines[1], "")
	stripped2 := ansiRE.ReplaceAllString(lines[2], "")
	idx1 := strings.Index(stripped1, "img:1")
	idx2 := strings.Index(stripped2, "img:2")
	if idx1 != idx2 {
		t.Errorf("IMAGE column misaligned: row1 at %d, row2 at %d\nRow1: %q\nRow2: %q", idx1, idx2, stripped1, stripped2)
	}
}

func TestFormatAppTable_Counts(t *testing.T) {
	apps := []api.AppDto{
		{Name: "b-app", Status: "RUNNING", Image: "img:1", Kind: "CITECK_CORE"},
		{Name: "a-app", Status: "FAILED", Image: "img:2", Kind: "CITECK_CORE"},
		{Name: "c-app", Status: "STARTING", Image: "img:3", Kind: "THIRD_PARTY"},
		{Name: "d-app", Status: "STOPPED", Image: "img:4", Kind: "THIRD_PARTY"},
		// STOPPING_FAILED must land in Failed, not Stopped — it's an error
		// path (docker stop errored), not a user-initiated detach. Mixing
		// it into Stopped would hide the failure behind a green
		// "reload complete" summary.
		{Name: "e-app", Status: "STOPPING_FAILED", Image: "img:5", Kind: "THIRD_PARTY"},
	}
	r := FormatAppTable(apps)
	if r.Total != 5 {
		t.Errorf("total = %d, want 5", r.Total)
	}
	if r.Running != 1 {
		t.Errorf("running = %d, want 1", r.Running)
	}
	if r.Failed != 2 {
		t.Errorf("failed = %d, want 2 (FAILED + STOPPING_FAILED)", r.Failed)
	}
	if r.Stopped != 1 {
		t.Errorf("stopped = %d, want 1 (only STOPPED — detached apps must be counted "+
			"as terminal or setup/reload wait loops hang forever)", r.Stopped)
	}
	if !strings.Contains(r.Table, "APP") {
		t.Error("table should contain header")
	}
	// Table should contain group headers
	stripped := ansiRE.ReplaceAllString(r.Table, "")
	if !strings.Contains(stripped, "Citeck Core") {
		t.Error("table should contain 'Citeck Core' group header")
	}
	if !strings.Contains(stripped, "Third Party") {
		t.Error("table should contain 'Third Party' group header")
	}
	// Within a group, apps should be sorted alphabetically
	aIdx := strings.Index(stripped, "a-app")
	bIdx := strings.Index(stripped, "b-app")
	if aIdx < 0 || bIdx < 0 || aIdx > bIdx {
		t.Errorf("apps should be sorted within group: a-app before b-app")
	}
}

func TestFormatAppTable_GroupOrder(t *testing.T) {
	apps := []api.AppDto{
		{Name: "postgres", Status: "RUNNING", Kind: "THIRD_PARTY"},
		{Name: "emodel", Status: "RUNNING", Kind: "CITECK_CORE"},
		{Name: "integrations", Status: "RUNNING", Kind: "CITECK_CORE_EXTENSION"},
		{Name: "attorneys", Status: "RUNNING", Kind: "CITECK_ADDITIONAL"},
	}
	r := FormatAppTable(apps)
	stripped := ansiRE.ReplaceAllString(r.Table, "")

	// Groups must appear in order: Core → Extensions → Additional → Third Party.
	// Search for each group label as the first non-whitespace content on its
	// line. "Citeck Core" must NOT match the "Citeck Core Extensions" line,
	// so we check that the trimmed line starts with the label and the next
	// char (if any) is a space (padding from the table formatter).
	lines := strings.Split(stripped, "\n")
	findLine := func(label string) int {
		for i, l := range lines {
			trimmed := strings.TrimSpace(l)
			if trimmed == label || strings.HasPrefix(trimmed, label+"  ") {
				return i
			}
		}
		return -1
	}
	coreIdx := findLine("Citeck Core")
	extIdx := findLine("Citeck Core Extensions")
	addIdx := findLine("Citeck Additional")
	tpIdx := findLine("Third Party")

	if coreIdx < 0 || extIdx < 0 || addIdx < 0 || tpIdx < 0 {
		t.Fatalf("missing group headers in table:\n%s", stripped)
	}
	if coreIdx >= extIdx || extIdx >= addIdx || addIdx >= tpIdx {
		t.Errorf("group order wrong: core=%d ext=%d add=%d tp=%d", coreIdx, extIdx, addIdx, tpIdx)
	}
}

func TestSetFormat_JSONDisablesColors(t *testing.T) {
	SetColorsEnabled(true)
	SetFormat(FormatJSON)

	result := Colorize(Green, "test")
	if strings.Contains(result, "\033[") {
		t.Error("colors should be disabled in JSON mode")
	}

	// Restore
	SetFormat(FormatText)
	SetColorsEnabled(true)
}

// B6-08: non-TTY stdout (pipes, redirects, CI) must drop ANSI escapes so
// that `citeck status | grep -cw RUNNING` works correctly. NO_COLOR
// (https://no-color.org/) must also disable colors.
func TestComputeColorsEnabled_NoColorEnvDisables(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if computeColorsEnabled() {
		t.Error("NO_COLOR=1 must disable colors")
	}
}

func TestComputeColorsEnabled_NonTTYDisables(t *testing.T) {
	// In `go test` stdout is a pipe (not a TTY) unless running under a
	// real terminal, so IsTTY() returns false here — verify that alone
	// drops colors even when NO_COLOR is unset.
	t.Setenv("NO_COLOR", "")
	if IsTTY() {
		t.Skip("stdout happens to be a TTY in this test environment; skipping non-TTY check")
	}
	if computeColorsEnabled() {
		t.Error("non-TTY stdout must disable colors")
	}
}

func TestColorize_NoANSIWhenColorsDisabled(t *testing.T) {
	prev := colorsEnabled
	SetColorsEnabled(false)
	defer SetColorsEnabled(prev)

	got := Colorize(Green, "RUNNING")
	if got != "RUNNING" {
		t.Errorf("expected bare text, got %q", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("unexpected ANSI escape in %q", got)
	}
}

func TestColorizeStatus_NoANSIWhenColorsDisabled(t *testing.T) {
	prev := colorsEnabled
	SetColorsEnabled(false)
	defer SetColorsEnabled(prev)

	got := ColorizeStatus("RUNNING")
	if strings.Contains(got, "\x1b[") {
		t.Errorf("ColorizeStatus must drop ANSI when colors are off, got %q", got)
	}
}

// A table whose headers are translated must line up with its own rows: the
// column width is a display width, not a byte count. Before this, "Зависимость"
// measured 22 and every row under it was padded 11 columns too far — the table
// was misaligned in five of the eight locales the CLI ships.
func TestFormatTable_NonASCIIHeadersAlign(t *testing.T) {
	prev := colorsEnabled
	SetColorsEnabled(false)
	defer SetColorsEnabled(prev)

	table := FormatTable(
		[]string{"Зависимость", "依存関係", "Status"},
		[][]string{{"postgres", "17.5", "up to date"}},
	)
	lines := strings.Split(table, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected a header and one row, got %q", table)
	}
	// The row's second column must start in the same column as the header's.
	headerCol := DisplayWidth("Зависимость") + 2
	rowCol := DisplayWidth("postgres") + strings.Index(lines[1][len("postgres"):], "17.5")
	if rowCol != headerCol {
		t.Errorf("second column starts at %d in the row and %d in the header:\n%s", rowCol, headerCol, table)
	}
}

func TestDisplayWidth(t *testing.T) {
	cases := map[string]int{
		"":            0,
		"abc":         3,
		"Зависимость": 11, // 22 bytes, 11 columns
		"依存関係":        8,  // 4 wide runes
		"ab依":         4,
	}
	for in, want := range cases {
		if got := DisplayWidth(in); got != want {
			t.Errorf("DisplayWidth(%q) = %d, want %d", in, got, want)
		}
	}
}

// An app parked in DEPS_WAITING by a DETACHED dependency will not move until
// the user starts that dependency again, so the CLI wait has to count it as
// settled. It is neither RUNNING nor FAILED nor STOPPED, so `citeck start`'s
// terminal check (running+failed+stopped == total) never matched and the
// command polled forever — a reachable state since a stopped dependency began
// holding its dependents.
func TestFormatAppTable_CountsAppsHeldByADetachedDependency(t *testing.T) {
	apps := []api.AppDto{
		{Name: "gateway", Status: "RUNNING"},
		{Name: "postgres", Status: "STOPPED"},
		{Name: "emodel", Status: "DEPS_WAITING", Held: true, WaitingFor: []api.WaitingDepDto{
			{App: "postgres", Status: "STOPPED"},
		}},
	}

	r := FormatAppTable(apps)

	if r.Held != 1 {
		t.Errorf("held = %d, want 1 (an app held by a stopped dependency is terminal)", r.Held)
	}
	if r.Running != 1 || r.Stopped != 1 || r.Total != 3 {
		t.Errorf("running/stopped/total = %d/%d/%d, want 1/1/3", r.Running, r.Stopped, r.Total)
	}
}

// A DEPS_WAITING app the daemon did NOT mark held is genuinely pending — the
// table must not decide otherwise on its own, or the two sides would answer the
// same question differently.
func TestFormatAppTable_AnUnmarkedDepsWaitingAppIsNotHeld(t *testing.T) {
	apps := []api.AppDto{
		{Name: "postgres", Status: "STARTING"},
		{Name: "emodel", Status: "DEPS_WAITING", WaitingFor: []api.WaitingDepDto{
			{App: "postgres", Status: "STARTING"},
		}},
	}

	r := FormatAppTable(apps)

	if r.Held != 0 {
		t.Errorf("held = %d, want 0 (the daemon did not mark it held)", r.Held)
	}
}

// The reason is on the wire (AppDto.WaitingFor) but `citeck status` rendered a
// bare DEPS_WAITING, so the operator saw a stuck app with no cause.
func TestFormatAppTable_DepsWaitingNamesWhatItWaitsFor(t *testing.T) {
	apps := []api.AppDto{
		{Name: "emodel", Status: "DEPS_WAITING", WaitingFor: []api.WaitingDepDto{
			{App: "postgres", Status: "STOPPED"},
			{App: "zookeeper", Status: "STARTING"},
		}},
	}

	r := FormatAppTable(apps)

	stripped := ansiRE.ReplaceAllString(r.Table, "")
	if !strings.Contains(stripped, "postgres") || !strings.Contains(stripped, "zookeeper") {
		t.Errorf("the STATUS cell must name what the app waits for, got:\n%s", stripped)
	}
}

// The apps to start are the DETACHED ones at the root of the holds, not the
// held apps themselves and not the links between them: an app held THROUGH
// another held app waits on something that is itself waiting, and naming that
// would send the operator to a link they cannot start.
func TestFormatAppTable_HeldDepsNamesTheDetachedRoots(t *testing.T) {
	apps := []api.AppDto{
		{Name: "zookeeper", Status: "STOPPED"},
		{Name: "gateway", Status: "DEPS_WAITING", Held: true, WaitingFor: []api.WaitingDepDto{
			{App: "zookeeper", Status: "STOPPED"},
		}},
		{Name: "proxy", Status: "DEPS_WAITING", Held: true, WaitingFor: []api.WaitingDepDto{
			{App: "gateway", Status: "DEPS_WAITING"},
		}},
	}

	r := FormatAppTable(apps)

	if r.Held != 2 {
		t.Errorf("held = %d, want 2", r.Held)
	}
	if len(r.HeldDeps) != 1 || r.HeldDeps[0] != "zookeeper" {
		t.Errorf("heldDeps = %v, want [zookeeper] -- the detached root only", r.HeldDeps)
	}
}

// A detached root can sit persistently in STOPPING_FAILED: StopApp records the
// detach in manualStoppedApps synchronously, BEFORE the stop can fail. Matching
// only "STOPPED" dropped it from the list, and the sentence built from that list
// then read "stopped dependencies: ." with nothing after the colon.
func TestFormatAppTable_HeldDepsCoverADetachedRootThatFailedToStop(t *testing.T) {
	apps := []api.AppDto{
		{Name: "zookeeper", Status: "STOPPING_FAILED"},
		{Name: "gateway", Status: "DEPS_WAITING", Held: true, WaitingFor: []api.WaitingDepDto{
			{App: "zookeeper", Status: "STOPPING_FAILED"},
		}},
	}

	r := FormatAppTable(apps)

	if len(r.HeldDeps) != 1 || r.HeldDeps[0] != "zookeeper" {
		t.Errorf("heldDeps = %v, want [zookeeper] -- a detached root stays the root however the stop ended", r.HeldDeps)
	}
}

// The namespace-wide answer and the per-app one only look alike while there is
// ONE detached root. With two independent ones, `citeck start emodel` used to
// name onlyoffice as well — an app that has nothing to do with emodel's hold,
// and starting it releases nothing.
func TestHeldRootsForApp_NamesOnlyTheRootsBehindThatApp(t *testing.T) {
	apps := []api.AppDto{
		{Name: "postgres", Status: "STOPPED"},
		{Name: "onlyoffice", Status: "STOPPED"},
		{Name: "emodel", Status: "DEPS_WAITING", Held: true,
			WaitingFor: []api.WaitingDepDto{{App: "postgres", Status: "STOPPED"}}},
		{Name: "proxy", Status: "DEPS_WAITING", Held: true,
			WaitingFor: []api.WaitingDepDto{{App: "onlyoffice", Status: "STOPPED"}}},
	}

	if got := HeldRootsForApp(apps, "emodel"); !slices.Equal(got, []string{"postgres"}) {
		t.Errorf("roots(emodel) = %v, want [postgres] -- onlyoffice holds a different app", got)
	}
	if got := HeldRootsForApp(apps, "proxy"); !slices.Equal(got, []string{"onlyoffice"}) {
		t.Errorf("roots(proxy) = %v, want [onlyoffice]", got)
	}
	if got := HeldDeps(apps); !slices.Equal(got, []string{"onlyoffice", "postgres"}) {
		t.Errorf("HeldDeps = %v, want [onlyoffice postgres] -- the namespace-wide question has a different answer", got)
	}
}

// The reason the single-app wait reached for the namespace-wide answer in the
// first place: on a transitive hold the app's OWN WaitingFor names an
// intermediate held app, which the operator never stopped and cannot start
// (RestartApp is a no-op on DEPS_WAITING). The walk must come out at the root.
func TestHeldRootsForApp_WalksThroughIntermediateHeldApps(t *testing.T) {
	apps := []api.AppDto{
		{Name: "zookeeper", Status: "STOPPED"},
		{Name: "gateway", Status: "DEPS_WAITING", Held: true,
			WaitingFor: []api.WaitingDepDto{{App: "zookeeper", Status: "STOPPED"}}},
		{Name: "proxy", Status: "DEPS_WAITING", Held: true,
			WaitingFor: []api.WaitingDepDto{{App: "gateway", Status: "DEPS_WAITING"}}},
	}

	if got := HeldRootsForApp(apps, "proxy"); !slices.Equal(got, []string{"zookeeper"}) {
		t.Errorf("roots(proxy) = %v, want [zookeeper] -- the operator never stopped gateway and cannot start it", got)
	}
}

// A cycle is refused at generation, but a hand-edited state file must not hang
// the CLI — the same guard the runtime's own walk carries.
func TestHeldRootsForApp_TerminatesOnACycle(t *testing.T) {
	apps := []api.AppDto{
		{Name: "a", Status: "DEPS_WAITING", Held: true,
			WaitingFor: []api.WaitingDepDto{{App: "b", Status: "DEPS_WAITING"}}},
		{Name: "b", Status: "DEPS_WAITING", Held: true,
			WaitingFor: []api.WaitingDepDto{{App: "a", Status: "DEPS_WAITING"}}},
	}

	if got := HeldRootsForApp(apps, "a"); len(got) != 0 {
		t.Errorf("roots(a) = %v, want [] -- a cycle has no root that can be named", got)
	}
}

// An app nobody is holding has no roots to name, and neither has one the DTO
// does not carry.
func TestHeldRootsForApp_AnswersNothingForAnAppThatIsNotHeld(t *testing.T) {
	apps := []api.AppDto{
		{Name: "postgres", Status: "STOPPED"},
		{Name: "emodel", Status: "DEPS_WAITING",
			WaitingFor: []api.WaitingDepDto{{App: "postgres", Status: "STOPPED"}}},
	}

	if got := HeldRootsForApp(apps, "emodel"); got != nil {
		t.Errorf("roots(emodel) = %v, want nil -- a lone DEPS_WAITING is not a hold", got)
	}
	if got := HeldRootsForApp(apps, "nosuchapp"); got != nil {
		t.Errorf("roots(nosuchapp) = %v, want nil", got)
	}
}
