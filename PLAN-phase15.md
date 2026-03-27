# Phase 15: Lens-Inspired UI Redesign

## Context

The current Web UI uses browser-style tab navigation — clicking an app name, logs, or config opens a new page-tab replacing the entire content area. The redesign adopts the Lens (Kubernetes IDE) pattern: the app table always stays visible, details and logs appear in overlay panels.

## Design Decisions

| Decision | Choice |
|----------|--------|
| Right drawer | **Overlay** mode — slides over table with semi-transparent backdrop |
| Drawer content | Container details + env + volumes (read-only inspect) |
| App config + files | Bottom panel tab (via gear icon or drawer button) |
| NS config (namespace.yml) | Bottom panel tab |
| Daemon logs | Bottom panel tab |
| App logs | Bottom panel tab |
| Welcome screen | Desktop mode only |

---

## Target Layout

```
┌──────────────────────────────────────────────────────┐
│ TabBar (simplified: only Volumes, Secrets, Diag, Wiz)│
├────────┬─────────────────────────────────────────────┤
│        │                                 ┌───────────┤
│Sidebar │  AppTable                       │ Drawer    │
│ w-52   │  (always visible, never moves)  │ (overlay) │
│ NS name│  Grouped by kind                │ w-[420px] │
│ ⚙ gear │                                │ absolute  │
│Controls│                                 │ right-0   │
│ Links  │                                 └───────────┤
├────────┴─────────────────────────────────────────────┤
│ ═══ drag handle ═════════════════════════════════════│
│ [▾] [Logs: eapps ✕] [Config: ns.yml ✕] [DaemonLog ✕]│
│                                                      │
│  Active tab content (streaming logs, YAML editor...) │
│                                                      │
└──────────────────────────────────────────────────────┘
```

---

## Sub-Phase 15a: Panel Infrastructure

**4 new files, no behavior changes.**

### 15a-1: `web/src/lib/panels.ts` — Panel state store

```typescript
interface BottomPanelTab {
  id: string           // "logs:eapps" | "ns-config" | "daemon-logs" | "app-config:emodel"
  type: 'logs' | 'ns-config' | 'daemon-logs' | 'app-config'
  title: string
  appName?: string     // for logs and app-config types
}

interface PanelState {
  drawerAppName: string | null
  bottomTabs: BottomPanelTab[]
  activeBottomTabId: string | null
  bottomPanelOpen: boolean
  bottomPanelHeight: number          // default 250, persisted to localStorage

  openDrawer(appName: string): void
  closeDrawer(): void
  openBottomTab(tab: BottomPanelTab): void
  closeBottomTab(id: string): void
  setActiveBottomTab(id: string): void
  setBottomPanelHeight(h: number): void
  toggleBottomPanel(): void
}
```

Key behaviors:
- `openDrawer("emodel")` while showing "postgres" → switches content (no close/reopen)
- `openBottomTab` with existing ID → activates it + sets `bottomPanelOpen = true`
- `closeBottomTab` on last tab → `bottomPanelOpen = false`
- Height loaded from `localStorage("citeck-bp-height")`, clamped 120..70%vh

### 15a-2: `web/src/hooks/useResizeHandle.ts`

Pointer-capture-based drag hook. Min 120px, max 70% viewport.
Returns `{ handleProps, isResizing }`.

### 15a-3: `web/src/components/BottomPanel.tsx`

- Drag handle (4px bar, highlights primary on hover/active)
- Tab strip: horizontal, close X per tab, collapse chevron
- Content area: renders active tab's component
- **Lazy mounting**: tab component created on first activation, stays mounted until closed
- Inactive tabs: `hidden` CSS (preserves state), `active={false}` prop to pause streaming
- When collapsed: only tab strip visible (~28px), content `h-0 overflow-hidden`
- Tab overflow: horizontal scroll on tab strip (`overflow-x-auto`)

### 15a-4: `web/src/components/RightDrawer.tsx`

Shell component:
- `position: absolute; right: 0; top: 0; bottom: 0` inside the content area
- Backdrop: `bg-black/20 absolute inset-0` (click to close)
- Drawer panel: `w-[420px] bg-card border-l border-border shadow-lg`
- Transition: `translate-x-full` → `translate-x-0` (200ms ease-out)
- Header: app name, StatusBadge (live from dashboard store), close X
- Body: scrollable, renders `<AppDrawerContent appName={...} />`

---

## Sub-Phase 15b: Extract Reusable Components

**5 new files + 4 existing files refactored. No behavior changes.**

### 15b-1: `components/LogViewer.tsx` (extracted from `Logs.tsx`)

Props: `appName: string`, `compact?: boolean`, `active?: boolean`
- `compact=true`: no header/breadcrumb, `h-full`, condensed toolbar
- `active=false`: aborts streaming, skips reconnect (pauses but keeps state)
- `Logs.tsx` → thin wrapper: `useParams` + `<LogViewer appName={name!} />`

### 15b-2: `components/ConfigEditor.tsx` (extracted from `Config.tsx`)

Props: `compact?: boolean`
- YAML viewer/editor + apply/cancel + confirm modal
- Includes `YamlViewer`, `YamlLine`, `YamlValue`
- `compact=true`: no health checks section, `h-full`
- `Config.tsx` → health checks section + `<ConfigEditor />`

### 15b-3: `components/DaemonLogsViewer.tsx` (extracted from `DaemonLogs.tsx`)

Props: `compact?: boolean`
- Polling + visibility pause/resume (existing behavior)
- `compact=true`: no page header
- `DaemonLogs.tsx` → thin wrapper

### 15b-4: `components/AppDrawerContent.tsx` (new, from inspect parts of `AppDetail.tsx`)

Props: `appName: string`
- Fetches `getAppInspect(appName)` on mount, with AbortController cleanup
- **Sections** (all read-only):
  - Container details grid (ID, image, state, network, started, uptime, restarts, ports)
  - Environment (scrollable, masked secrets with `=***`)
  - Volumes list
- **Action buttons** at bottom:
  - "View Logs" → `openBottomTab({ type: 'logs', appName })`
  - "Edit Config" → `openBottomTab({ type: 'app-config', appName })`
  - "Restart" → `postAppRestart` + toast
- **Live status**: reads `useDashboardStore.namespace.apps` for SSE-updated status badge
- **Handles STOPPED apps**: shows available fields, grays out unavailable (no container ID, no uptime)

### 15b-5: `components/AppConfigEditor.tsx` (new, from config/files parts of `AppDetail.tsx`)

Props: `appName: string`
- Self-contained: fetches `getAppConfig` + `getAppFiles` on mount
- App YAML editor: view/edit/apply/lock-unlock flow
- Mounted files list: per-file edit button, inline editor, save/cancel
- Confirmation modal for apply
- Handles missing config gracefully ("No custom config" state)

### After extraction: `AppDetail.tsx` (fallback for deep links)

Composes `<AppDrawerContent>` + `<AppConfigEditor>` + recent logs.
All existing functionality preserved for `/apps/:name` URL.

---

## Sub-Phase 15c: Integrate Panels into Dashboard

**This is where behavior changes.**

### 15c-1: Fix Dashboard height management

**Problem**: Currently `App.tsx` has `<main className="flex-1 min-h-0 overflow-auto">`. The Dashboard returns a `flex h-full` div, but the parent's `overflow-auto` means the Dashboard can scroll beyond viewport. For the bottom panel to stick to the bottom, Dashboard must fill exactly the available height, with its own internal overflow.

**Fix in `App.tsx`**: Change `<main>` to `<main className="flex-1 min-h-0">` (remove `overflow-auto`). Let each page manage its own overflow. Dashboard uses `h-full overflow-hidden`. Other pages keep their own scroll behavior.

### 15c-2: Restructure `Dashboard.tsx`

```tsx
<div className="flex flex-col h-full overflow-hidden">
  {/* Top: sidebar + table + drawer overlay */}
  <div className="flex flex-1 min-h-0 relative">
    <aside className="w-52 shrink-0 ...sidebar styles...">
      {/* existing sidebar + gear icon next to NS name (15c-6) */}
    </aside>
    <div className="flex-1 min-w-0 p-2 overflow-auto">
      <AppTable apps={namespace.apps} highlightedApp={drawerAppName} />
    </div>
    {drawerAppName && <RightDrawer appName={drawerAppName} onClose={closeDrawer} />}
  </div>
  {/* Bottom panel */}
  {bottomTabs.length > 0 && <BottomPanel />}
</div>
```

### 15c-3: Rewire `AppTable.tsx` click handlers

Replace `openInTab + navigate` with panel actions:
- **App name click**: `openDrawer(app.name)`
- **Logs icon click**: `openBottomTab({ id: 'logs:'+name, type: 'logs', title: 'Logs: '+name, appName })`
- **Settings icon click**: `openBottomTab({ id: 'app-config:'+name, type: 'app-config', title: 'Config: '+name, appName })`

Add `highlightedApp` prop: row with matching name gets `bg-primary/10`.

Remove `useNavigate` and `useTabsStore` from `GroupRows`.

### 15c-4: Sidebar "Launcher Logs" → bottom panel

```tsx
// Before:
openTab({ id: 'daemon-logs', ... }); navigate('/daemon-logs')
// After:
openBottomTab({ id: 'daemon-logs', type: 'daemon-logs', title: 'Daemon Logs' })
```

Volumes, Secrets, Diagnostics stay as routed pages (they're complex standalone pages).

### 15c-5: TabBar settings icon → bottom panel

```tsx
// Before:
openTab({ id: 'config', ... }); navigate('/config')
// After:
openBottomTab({ id: 'ns-config', type: 'ns-config', title: 'Config: ns.yml' })
```

### 15c-6: Namespace settings gear icon in sidebar

Add a gear icon (⚙) next to the namespace name in the sidebar header:
```tsx
<div className="flex items-center justify-between">
  <div>
    <div className="text-sm font-semibold">{namespace.name}</div>
    <div className="text-[11px] text-muted-foreground">{namespace.bundleRef}</div>
  </div>
  <button title="Namespace config"
    onClick={() => openBottomTab({ id: 'ns-config', type: 'ns-config', title: 'Config: ns.yml' })}>
    <Settings size={14} />
  </button>
</div>
```

This matches the old Kotlin launcher's gear icon next to the namespace name.

### 15c-7: Clean up `lib/tabs.ts`

Remove creation of: `app-*`, `logs-*`, `config`, `daemon-logs` tab IDs.
Keep: `home`, `volumes`, `secrets`, `diagnostics`, `wizard`.

---

## Sub-Phase 15d: Polish

### 15d-1: Keyboard shortcuts
- `Escape` → close drawer if open; if drawer closed, collapse bottom panel

### 15d-2: Bottom panel height persistence
- Load from `localStorage` on store creation
- Save on pointer-up (resize end)

### 15d-3: Active-tab-only streaming
- BottomPanel passes `active={tab.id === activeBottomTabId}` to each tab's LogViewer
- LogViewer `active=false` → abort stream, don't reconnect
- LogViewer `active=true` → resume streaming

### 15d-4: Drawer row highlight
- AppTable receives `highlightedApp?: string` prop
- Matching row gets `bg-primary/10` background

---

## Sub-Phase 15e: Server Mode

### 15e-1: Backend — add `desktop` to DaemonStatusDto

`internal/api/dto.go`:
```go
type DaemonStatusDto struct {
    // ...existing...
    Desktop bool `json:"desktop"`
}
```

`internal/daemon/routes.go` (`handleDaemonStatus`):
```go
Desktop: config.IsDesktopMode(),
```

### 15e-2: Frontend — conditional welcome

Fetch daemon status once on app load. Store `isDesktopMode` flag.
- `desktop=false` (server mode): root `/` always shows Dashboard (never Welcome)
- `desktop=true`: root `/` shows Welcome when no namespace, Dashboard otherwise

---

## New Files (9)

| File | Est. Lines | Purpose |
|------|-----------|---------|
| `lib/panels.ts` | 80 | Zustand panel store |
| `hooks/useResizeHandle.ts` | 50 | Drag-to-resize |
| `components/BottomPanel.tsx` | 130 | Panel container + tabs |
| `components/RightDrawer.tsx` | 70 | Overlay drawer shell |
| `components/LogViewer.tsx` | 490 | Extracted log viewer |
| `components/ConfigEditor.tsx` | 180 | Extracted NS config editor |
| `components/DaemonLogsViewer.tsx` | 40 | Extracted daemon logs |
| `components/AppDrawerContent.tsx` | 150 | Drawer body |
| `components/AppConfigEditor.tsx` | 160 | App config + files editor |

## Modified Files (9)

| File | Change |
|------|--------|
| `App.tsx` | Remove `overflow-auto` from main, add server mode logic |
| `Dashboard.tsx` | 3-section layout (sidebar + table/drawer + bottom panel), gear icon |
| `AppTable.tsx` | Click handlers → panels, `highlightedApp` prop |
| `TabBar.tsx` | Settings → bottom panel |
| `Logs.tsx` | Thin wrapper → LogViewer |
| `Config.tsx` | Use ConfigEditor component |
| `DaemonLogs.tsx` | Thin wrapper → DaemonLogsViewer |
| `AppDetail.tsx` | Compose extracted components |
| `lib/tabs.ts` | Remove panel-ized tab types |

**Backend (2 files):** `internal/api/dto.go`, `internal/daemon/routes.go` — add `desktop` field.

## Unchanged

api.ts, store.ts, types.ts (add `desktop` field only), toast.ts, websocket.ts, Welcome, Wizard, Secrets, Volumes, Diagnostics, ConfirmModal, ContextMenu, StatusBadge, NamespaceControls, Toast, ErrorBoundary

---

## Execution

```
15a (infra) → 15b (extract) → 15c (integrate) → 15d (polish) → 15e (server mode)
```

After each: `npx vitest run` + browser test.
After 15c: `make build` + deploy to server + browser test.

---

## Verification

1. Click app name → drawer slides in from right over table
2. Click another app → drawer updates (no close/reopen animation)
3. Click backdrop or X → drawer closes
4. Click logs icon → bottom panel opens with streaming logs
5. Click settings gear in row → bottom panel with app config editor
6. Open 3+ log tabs → switch tabs, only active streams, scroll position preserved
7. Drag resize handle → panel height changes
8. Collapse chevron → content hidden, tab strip visible
9. Close all bottom tabs → panel disappears
10. Sidebar gear icon → opens NS config in bottom panel
11. TabBar settings → same as #10
12. Sidebar "Launcher Logs" → opens daemon logs in bottom panel
13. Escape → closes drawer or collapses panel
14. Drawer app row highlighted in table
15. Navigate to Volumes tab → panels hidden (Dashboard unmounted), come back → panels restore
16. Deep link `/apps/emodel` → full-page AppDetail (fallback)
17. Server mode (desktop=false) → no Welcome, straight to Dashboard
18. Desktop mode → Welcome when no namespace

## Edge Cases

- **STOPPED app in drawer**: show available fields, gray out unavailable (no container, no uptime)
- **No app config**: AppConfigEditor shows "No custom config" with optional create button
- **Panel + drawer both open**: drawer overlays table only (above table, within top section), bottom panel below
- **Many bottom tabs (8+)**: tab strip scrolls horizontally
- **Small viewport**: bottom panel max 50%vh on screens <700px tall
