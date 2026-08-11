import { postNamespaceStart, postNamespaceStop } from '../lib/api'
import { useDashboardStore } from '../lib/store'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'
import { showError } from '../lib/errorModal'
import { ContextMenu, type ContextMenuItem } from './ContextMenu'
import { useContextMenu } from '../hooks/useContextMenu'
import { useRegistryPreflight } from './RegistryPreflight'
import { Play, Square, Loader2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'

interface NamespaceControlsProps {
  status: string
}

// No 'reload' variant: the primary button always posts /namespace/start (see
// primaryAction below) and nothing else here dispatched a reload, so keeping it
// would just leave a re-wireable path back to the bug it caused.
type Action = 'start' | 'stop' | 'forceStart'

const actionFns: Record<Action, () => Promise<unknown>> = {
  start: () => postNamespaceStart(false),
  forceStart: () => postNamespaceStart(true),
  stop: postNamespaceStop,
}

// Safety net for the local click echo: if neither the server flag nor a status
// change ever arrives (daemon died, SSE wedged), the button must not stay
// disabled forever.
const CLICK_ECHO_TIMEOUT_MS = 30_000

// Module scope, not a ref: a failure stays in the DTO until the next click, so a
// component-scoped marker would re-raise the same modal on every remount (tab
// switch, navigation). Keyed by the daemon's occurrence timestamp.
let lastShownUpdateErrorAt = 0

export function NamespaceControls({ status }: NamespaceControlsProps) {
  const fetchData = useDashboardStore((s) => s.fetchData)
  // Server-side "accepted, working on it" flag. Covers the whole pre-runtime
  // stretch (reloadMu wait, git pull, bundle resolve, generate) during which
  // `status` is deliberately unchanged, and — unlike a purely local flag —
  // it also shows up in other windows and survives a page reload.
  const updating = useDashboardStore((s) => s.namespace?.updating ?? false)
  // Local echo bridging the few ms between the click and that flag coming back
  // over SSE/refetch, so the button reacts on the very first frame.
  const [clickEcho, setClickEcho] = useState(false)
  const echoTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  function stopEcho() {
    setClickEcho(false)
    if (echoTimer.current) {
      clearTimeout(echoTimer.current)
      echoTimer.current = null
    }
  }

  // Hand off from the local echo to the authoritative signals as soon as either
  // one shows up: the daemon's updating flag, or the runtime actually moving.
  // Adjusted during render — React's documented way to derive state from
  // changed inputs — rather than in an effect, which would cascade an extra
  // render (and trips react-hooks/set-state-in-effect).
  // Only the state is touched here, never the timer ref (reading a ref during
  // render is not allowed): a pending timeout that fires later just sets the
  // echo false a second time, and any new click resets the timer anyway.
  if (clickEcho && (updating || status === 'STARTING' || status === 'STOPPING')) setClickEcho(false)

  useEffect(() => () => { if (echoTimer.current) clearTimeout(echoTimer.current) }, [])

  const { t } = useTranslation()
  // A pass that fails leaves the UI byte-identical to one that succeeded — the
  // spinner stops and nothing changed — so surface it on the SAME modal the
  // synchronous failure path uses (exec's catch below). Same action, same
  // severity, same surface. Shown once per occurrence.
  const updateError = useDashboardStore((s) => s.namespace?.updateError ?? '')
  const updateErrorAt = useDashboardStore((s) => s.namespace?.updateErrorAt ?? 0)
  useEffect(() => {
    if (!updateError || updateErrorAt === lastShownUpdateErrorAt) return
    lastShownUpdateErrorAt = updateErrorAt
    showError({ title: t('ns.error.updateFailed'), message: updateError })
  }, [updateError, updateErrorAt, t])
  const { contextMenu, showContextMenu, hideContextMenu } = useContextMenu()
  // Pre-start registry-credentials gate (hard block) for pull-capable actions.
  const { preflight, dialog: registryPreflightDialog } = useRegistryPreflight()

  const isStopped = status === 'STOPPED'
  const isRunning = status === 'RUNNING' || status === 'STALLED'
  const isStarting = status === 'STARTING'
  // Kotlin parity: stop button is disabled when the namespace is already stopped.
  const stopEnabled = !isStopped
  // Kotlin parity: primary (Update&Start) is clickable while stopped or running.
  // While STARTING/STOPPING, the only safe operation is stop.
  const primaryEnabled = isStopped || isRunning
  // Kotlin parity: ALWAYS update-and-start, whatever the current status.
  // NamespaceRuntime.runtimeThreadAction() handles StartNsCmd without any
  // branch on nsStatus — a running namespace takes the identical path as a
  // stopped one (git pull under the ALLOWED policy, i.e. throttled by the
  // pull period → generateNs → re-drive every non-detached app, recreating
  // only the containers whose deployment hash actually changed).
  //
  // This used to route the running case to /namespace/reload, which is
  // doReloadEx(forceGitPull=false, startNotRegenerate=false, refreshImages=
  // FALSE). With refreshImages=false the :snapshot digest refresh is skipped,
  // so every hash is recomputed from the stale local digest, matches the
  // running container, and doRegenerate no-ops on every app — the click did
  // nothing but blip RUNNING→STARTING→RUNNING. /namespace/start passes
  // refreshImages=true and derives Start-vs-Regenerate from the live status
  // server-side (handleStartNamespace), so one action covers both states.
  const primaryAction: Action = 'start'
  // "Your click landed and work is happening." Either source alone leaves a
  // hole: the echo alone would lie about other clients and die on reload, the
  // server flag alone has an SSE round-trip of latency at the very moment the
  // user is looking for a reaction.
  const busy = clickEcho || updating

  async function exec(a: Action) {
    // Start the echo here rather than in run(): a click that is still sitting
    // behind the registry-credentials preflight dialog has not been sent
    // anywhere yet, and that dialog is its own feedback.
    if (a === 'start' || a === 'forceStart') {
      setClickEcho(true)
      if (echoTimer.current) clearTimeout(echoTimer.current)
      echoTimer.current = setTimeout(() => { setClickEcho(false); echoTimer.current = null }, CLICK_ECHO_TIMEOUT_MS)
    }
    try {
      await actionFns[a]()
      const toastAction = a === 'forceStart' ? 'start' : a
      toast(t('ns.toast.success', { action: toastAction }), 'success')
      // The daemon raises `updating` synchronously before answering, so this
      // refetch already picks it up; the SSE namespace_updating event is the
      // belt-and-braces path when the response races the flag.
      //
      // Release the echo once that refetch lands, rather than relying on the
      // 30s backstop. A pass can finish in microseconds — both skip paths
      // (no runtime, namespace switched) and a fast doReloadEx error do — and
      // then `updating` is already false and the status never moves, so nothing
      // would clear the echo and the button would sit disabled with a spinner
      // for the full timeout. Releasing here is safe in both directions: if the
      // pass IS still running the refetch returns updating:true and that keeps
      // the button busy on its own.
      setTimeout(() => { void fetchData().finally(stopEcho) }, 500)
    } catch (err) {
      // The request itself failed — nothing is running server-side, so drop the
      // echo immediately instead of leaving a button that looks busy.
      stopEcho()
      const e = err as Error
      showError({
        title: t(`ns.confirm.${a}.title`),
        message: e.message,
        details: e.stack,
      })
    }
  }

  // Fire start / stop immediately. The ConfirmModal that used to
  // gate every click was double-bookkeeping for actions the user had already
  // explicitly clicked; errors go to the global error modal as before.
  // Pull-capable actions first clear the registry-credentials pre-flight gate
  // so they don't start only to stall mid-pull; stop never pulls.
  async function run(a: Action) {
    if (a === 'stop') {
      await exec(a)
      return
    }
    await preflight(() => exec(a))
  }

  function primaryContextItems(): ContextMenuItem[] {
    return [
      { label: t('ns.forceStart'), onClick: () => { void run('forceStart') } },
    ]
  }

  return (
    <>
      {/* min-h instead of fixed h-7 so long locale labels (e.g. RU
          "Обновить и запустить") wrap to a centered second line instead of
          being clipped; short labels keep the single-row height. */}
      <div className="flex items-stretch min-h-[28px] rounded border border-border overflow-hidden">
        <button
          type="button"
          disabled={!primaryEnabled || isStarting || busy}
          className={`flex items-center justify-center gap-1.5 px-2 py-1 text-xs leading-tight text-center border-r border-border ${
            primaryEnabled && !isStarting && !busy
              ? 'text-success hover:bg-success/10'
              : 'text-muted-foreground/40 cursor-not-allowed'
          }`}
          style={{ flex: 7 }}
          onClick={() => { void run(primaryAction) }}
          // Deliberately NOT gated on `busy`: escalating a plain Update & Start
          // to a Force while it is still in flight is exactly what the daemon's
          // folding queue supports (the force flag is OR-ed into the queued
          // pass), and gating it here would make that path unreachable from the
          // UI.
          onContextMenu={(e) => { e.preventDefault(); if (primaryEnabled && !isStarting) showContextMenu(e, primaryContextItems()) }}
          title={busy ? t('ns.updating') : t('ns.updateAndStart')}
        >
          {/* Only the icon swaps; the label is deliberately left alone. Replacing
              the text would resize the control — RU measures 53px for the
              wrapped "Обновить и запустить" against 26px for "Обновление…", so
              the whole toolbar row would jump on every click — and the progress
              word does not even fit beside an icon at this width (the button is
              ~95px in RU, and "Обновление…" is one unbreakable word that
              overflows and clips the spinner). The spinning icon plus the
              disabled styling carries the "working on it" signal; the wording
              lives in the tooltip. */}
          {busy
            ? <Loader2 size={12} className="shrink-0 animate-spin" />
            : <Play size={12} className="shrink-0" />}
          {' '}{t('ns.updateAndStart')}
        </button>
        <button
          type="button"
          disabled={!stopEnabled}
          className={`flex items-center justify-center gap-1.5 px-2 py-1 text-xs leading-tight text-center ${
            stopEnabled
              ? 'text-destructive hover:bg-destructive/10'
              : 'text-muted-foreground/40 cursor-not-allowed'
          }`}
          style={{ flex: 3 }}
          onClick={() => { void run('stop') }}
          title={t('ns.stop')}
        >
          <Square size={12} className="shrink-0" /> {t('ns.stop')}
        </button>
      </div>

      {contextMenu && (
        <ContextMenu items={contextMenu.items} position={contextMenu.position} onClose={hideContextMenu} />
      )}
      {registryPreflightDialog}
    </>
  )
}
