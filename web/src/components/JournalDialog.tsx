import { useEffect, useRef, useState, useMemo } from 'react'
import { Loader2, Search, X, Plus } from 'lucide-react'
import { LoadingLabel } from './LoadingLabel'
import { useTranslation } from '../lib/i18n'

/**
 * Cell renderer: receives the raw row + the resolved string from `row[col.key]`.
 * Returning a ReactNode lets callers render badges, icons, or custom layout.
 */
export interface JournalColumn<T = Record<string, unknown>> {
  label: string
  key: string
  width?: string
  render?: (row: T) => React.ReactNode
}

/**
 * Per-row action — typically rendered as an icon button in the trailing
 * "Actions" column. Mirrors Kotlin's `EntityInfo.actions: List<ActionDesc>`.
 *
 * - `enabledIf` returning false greys out the button (still visible, mirrors
 *   Kotlin behaviour where edit/delete don't disappear for default entities;
 *   they just no-op).
 * - `decoration` adds a small visual marker — e.g. "edited" blue dot, or a
 *   count badge — without forcing the caller to render the whole cell.
 */
export interface JournalAction<T = Record<string, unknown>> {
  icon: React.ComponentType<{ size?: number; className?: string }>
  title: string
  onClick: (row: T) => void | Promise<void>
  enabledIf?: (row: T) => boolean
  /** Optional decoration: "blue dot" or count badge over the icon. */
  decoration?: (row: T) => { dot?: boolean; badge?: string | number } | null
  /** Variant for hover color: 'default' (muted-foreground), 'danger' (destructive). */
  variant?: 'default' | 'danger'
}

/**
 * Bottom-bar custom button. Mirrors Kotlin `JournalButton`:
 *  - `enabledIf`: enable predicate. When the selectable list is non-empty
 *    you can receive the selected rows to decide.
 *  - `loading`: when true, the caller's onClick is wrapped in a "loading"
 *    state — useful for actions that take seconds (delete all, snapshot).
 */
export interface JournalCustomButton<T = Record<string, unknown>> {
  label: string
  onClick: (selected: T[]) => void | Promise<void>
  enabledIf?: (selected: T[]) => boolean
  loading?: boolean
  variant?: 'default' | 'primary' | 'danger'
}

interface JournalDialogProps<T extends Record<string, unknown>> {
  title: string
  columns: JournalColumn<T>[]
  data: T[]
  open: boolean
  onClose: () => void
  /** If true, rows are selectable via checkbox. */
  selectable?: boolean
  /** If true (and selectable), multi-select; else single-select (clicking a row
   *  clears the others). Both render as checkboxes. */
  multiple?: boolean
  /** Row keys (first-column value) to pre-select whenever the data changes —
   *  e.g. the currently-active record, so it shows as a checked checkbox. */
  initialSelectedKeys?: string[]
  onSelect?: (selected: T[]) => void
  rowActions?: JournalAction<T>[]
  customButtons?: JournalCustomButton<T>[]
  /** When provided, shows a "+ Create" button in the footer. */
  onCreate?: () => void
  /** When true, auto-closes when the table becomes empty (Kotlin `closeWhenAllRecordsDeleted`). */
  closeWhenEmpty?: boolean
  /** Hide the search/filter input row (these dialogs show short lists where the
   *  filter is just clutter). Filtering logic stays inert with an empty query. */
  hideSearch?: boolean
  /** Shows a spinner row instead of the "no data" placeholder while data
   *  is being fetched, so the dialog doesn't flash "Нет данных" on open
   *  for caller fetches that take a few hundred ms. */
  loading?: boolean
}

/**
 * JournalDialog is the Web port of Kotlin's JournalSelectDialog
 * (view/form/components/journal/JournalSelectDialog.kt).
 *
 * It is generic over the row type so callers don't have to cast cell values.
 * Per-row actions mirror Kotlin's `EntityInfo.actions` model; the column
 * `render` callback handles custom cell content (size badges etc.).
 */
export function JournalDialog<T extends Record<string, unknown>>({
  title,
  columns,
  data,
  open,
  onClose,
  selectable = false,
  multiple = false,
  initialSelectedKeys,
  onSelect,
  rowActions,
  customButtons,
  onCreate,
  closeWhenEmpty = false,
  loading = false,
  hideSearch = false,
}: JournalDialogProps<T>) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<Set<string>>(() => new Set(initialSelectedKeys ?? []))
  const [buttonLoading, setButtonLoading] = useState<string | null>(null)

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    else if (!open && dialog.open) dialog.close()
  }, [open])

  // Reset selection when data identity changes (e.g. caller refreshes), seeding
  // it with the caller's pre-selected keys (the active record).
  const [prevData, setPrevData] = useState(data)
  if (data !== prevData) {
    setPrevData(data)
    setSelected(new Set(initialSelectedKeys ?? []))
  }

  // Kotlin parity: auto-close when the table is emptied. Used by namespace
  // picker after the last namespace is deleted (`closeWhenAllRecordsDeleted=true`).
  useEffect(() => {
    if (closeWhenEmpty && open && data.length === 0) {
      onClose()
    }
  }, [closeWhenEmpty, open, data.length, onClose])

  function rowKey(row: T): string {
    const firstCol = columns[0]
    return firstCol ? String(row[firstCol.key] ?? '') : JSON.stringify(row)
  }

  const filteredData = useMemo(() => {
    if (!search.trim()) return data
    const lower = search.toLowerCase()
    return data.filter((row) =>
      columns.some((col) => {
        const val = row[col.key]
        return val != null && String(val).toLowerCase().includes(lower)
      }),
    )
  }, [data, search, columns])

  function toggleRow(row: T) {
    const key = rowKey(row)
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(key)) {
        next.delete(key)
      } else {
        if (!multiple) next.clear()
        next.add(key)
      }
      return next
    })
  }

  function toggleAll() {
    if (selected.size === filteredData.length) {
      setSelected(new Set())
    } else {
      setSelected(new Set(filteredData.map((row) => rowKey(row))))
    }
  }

  function getSelectedRows(): T[] {
    return filteredData.filter((row) => selected.has(rowKey(row)))
  }

  function handleSelect() {
    if (onSelect) onSelect(getSelectedRows())
    onClose()
  }

  function handleRowDoubleClick(row: T) {
    if (!selectable) return
    if (!multiple) {
      // Kotlin parity: double-click on the name cell submits in single-select.
      if (onSelect) onSelect([row])
      onClose()
    }
  }

  async function runCustomButton(btn: JournalCustomButton<T>) {
    if (btn.loading) setButtonLoading(btn.label)
    try {
      await btn.onClick(getSelectedRows())
    } finally {
      setButtonLoading(null)
    }
  }

  return (
    <dialog
      ref={dialogRef}
      className="z-50 fixed rounded-lg border border-border bg-card p-0 text-foreground shadow-xl"
      style={{
        width: 'min(90vw, 768px)',
        // Dialog auto-grows to its content; the table body has its own
        // max-height + scroll, and the empty/loading row a min-height so
        // a short list doesn't add padding and an empty list doesn't
        // collapse to zero.
        maxHeight: 'min(80vh, 720px)',
        top: '50%',
        left: '50%',
        transform: 'translate(-50%, -50%)',
        margin: 0,
      }}
      onClose={onClose}
    >
      <div className="flex flex-col" style={{ width: '100%' }}>
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-border shrink-0">
          <h2 className="text-sm font-semibold">{title}</h2>
          <button
            type="button"
            className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted"
            onClick={onClose}
            aria-label={t('common.close')}
            title={t('common.close')}
          >
            <X size={16} />
          </button>
        </div>

        {/* Search */}
        {!hideSearch && (
          <div className="px-4 py-2 border-b border-border shrink-0">
            <div className="relative">
              <Search size={14} className="absolute left-2 top-1/2 -translate-y-1/2 text-muted-foreground" />
              <input
                type="text"
                className="w-full rounded border border-border bg-background pl-7 pr-2 py-1 text-xs focus:outline-none focus:border-primary"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t('journal.filter')}
              />
            </div>
          </div>
        )}

        {/* Table — own scroll viewport so the dialog grows naturally with
            short lists but caps when the list is long enough to fill it. */}
        <div className="overflow-auto px-4" style={{ maxHeight: 'calc(min(80vh, 720px) - 140px)' }}>
          <table className="w-full text-xs border-collapse">
            <thead className="sticky top-0 bg-card">
              <tr className="text-left text-muted-foreground border-b border-border">
                {selectable && (
                  <th className="py-1.5 pr-2 w-8 font-medium">
                    {multiple && (
                      <input
                        type="checkbox"
                        className="rounded border-border"
                        checked={filteredData.length > 0 && selected.size === filteredData.length}
                        onChange={toggleAll}
                      />
                    )}
                  </th>
                )}
                {columns.map((col) => (
                  <th key={col.key} className="py-1.5 pr-4 font-medium" style={col.width ? { width: col.width } : undefined}>
                    {col.label}
                  </th>
                ))}
                {rowActions && rowActions.length > 0 && (
                  <th className="py-1.5 pr-0 text-right font-medium">{t('journal.actions')}</th>
                )}
              </tr>
            </thead>
            <tbody>
              {filteredData.map((row, i) => {
                const key = rowKey(row)
                const isSelected = selected.has(key)
                return (
                  <tr
                    key={key || i}
                    className={`border-b border-border/20 hover:bg-muted/30 ${isSelected ? 'bg-primary/5' : ''} ${selectable ? 'cursor-pointer' : ''}`}
                    onClick={selectable ? () => toggleRow(row) : undefined}
                    onDoubleClick={() => handleRowDoubleClick(row)}
                  >
                    {selectable && (
                      <td className="py-[3px] pr-2">
                        <input
                          type="checkbox"
                          className="rounded border-border"
                          checked={isSelected}
                          onChange={() => toggleRow(row)}
                          onClick={(e) => e.stopPropagation()}
                        />
                      </td>
                    )}
                    {columns.map((col) => (
                      <td key={col.key} className="py-[3px] pr-4 text-muted-foreground">
                        {col.render ? col.render(row) : (row[col.key] != null ? String(row[col.key]) : '')}
                      </td>
                    ))}
                    {rowActions && rowActions.length > 0 && (
                      <td className="py-[3px] pr-0 text-right whitespace-nowrap">
                        {rowActions.map((act, idx) => {
                          const enabled = act.enabledIf ? act.enabledIf(row) : true
                          const deco = act.decoration ? act.decoration(row) : null
                          const Icon = act.icon
                          // Only colour the hover when the action is actually
                          // clickable. A disabled danger button that still turns
                          // red on hover reads as "active but broken" — the user
                          // clicks and nothing happens. Gate hover on `enabled`
                          // and show a not-allowed cursor instead.
                          const hover = !enabled
                            ? 'cursor-not-allowed'
                            : `hover:bg-muted ${act.variant === 'danger' ? 'hover:text-destructive' : 'hover:text-foreground'}`
                          return (
                            <button
                              key={idx}
                              type="button"
                              className={`relative inline-flex items-center justify-center p-1 rounded text-muted-foreground ${hover} disabled:opacity-40 disabled:hover:bg-transparent`}
                              title={act.title}
                              disabled={!enabled}
                              onClick={(e) => { e.stopPropagation(); void act.onClick(row) }}
                            >
                              <Icon size={14} />
                              {deco?.dot && <span className="absolute top-0.5 right-0.5 h-1.5 w-1.5 rounded-full bg-primary" />}
                              {deco?.badge != null && (
                                <span className="absolute -bottom-0.5 -right-0.5 text-[9px] font-medium bg-primary text-primary-foreground rounded px-0.5">
                                  {deco.badge}
                                </span>
                              )}
                            </button>
                          )
                        })}
                      </td>
                    )}
                  </tr>
                )
              })}
              {filteredData.length === 0 && (
                <tr>
                  <td
                    colSpan={columns.length + (selectable ? 1 : 0) + (rowActions && rowActions.length > 0 ? 1 : 0)}
                    className="text-center text-muted-foreground"
                    style={{ height: 120 }}
                  >
                    {loading ? (
                      <span className="inline-flex items-center gap-2">
                        <Loader2 size={14} className="animate-spin" />
                        {t('common.loading')}
                      </span>
                    ) : search ? t('journal.noMatchingRows') : t('journal.noData')}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between px-4 py-3 border-t border-border shrink-0 gap-2 flex-wrap">
          <span className="text-[11px] text-muted-foreground">
            {filteredData.length === 1
              ? t('journal.rowCountOne')
              : t('journal.rowCount', { count: filteredData.length })}
            {selectable && selected.size > 0 && ` (${t('journal.selected', { count: selected.size })})`}
          </span>
          <div className="flex items-center gap-2 flex-wrap">
            {onCreate && (
              <button
                type="button"
                className="inline-flex items-center gap-1 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted"
                onClick={onCreate}
              >
                <Plus size={12} /> {t('common.create')}
              </button>
            )}
            {customButtons?.map((btn) => {
              const enabled = btn.enabledIf ? btn.enabledIf(getSelectedRows()) : true
              const isDisabled = !enabled || buttonLoading === btn.label
              // Gate the hover background on the enabled state — :hover still
              // fires on disabled buttons, so an always-on hover class makes a
              // disabled "Delete All" look clickable when it isn't.
              const variant =
                btn.variant === 'primary' ? `bg-primary text-primary-foreground ${isDisabled ? '' : 'hover:bg-primary/90'}`
                : btn.variant === 'danger' ? `border border-destructive/40 text-destructive ${isDisabled ? '' : 'hover:bg-destructive/10'}`
                : `border border-border ${isDisabled ? '' : 'hover:bg-muted'}`
              return (
                <button
                  key={btn.label}
                  type="button"
                  className={`rounded-md px-3 py-1.5 text-xs disabled:opacity-50 ${isDisabled ? 'cursor-not-allowed' : ''} ${variant}`}
                  disabled={isDisabled}
                  onClick={() => runCustomButton(btn)}
                >
                  <LoadingLabel loading={buttonLoading === btn.label}>{btn.label}</LoadingLabel>
                </button>
              )
            })}
            {selectable && onSelect && (
              <button
                type="button"
                className="rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-xs font-medium hover:bg-primary/90 disabled:opacity-50"
                onClick={handleSelect}
                disabled={selected.size === 0}
              >
                {t('common.select')}
              </button>
            )}
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted"
              onClick={onClose}
            >
              {t('common.close')}
            </button>
          </div>
        </div>
      </div>
    </dialog>
  )
}
