import { useEffect, useRef, useState, useMemo } from 'react'
import { Search, X, Plus } from 'lucide-react'
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
  /** If true, rows are selectable via checkbox/radio. */
  selectable?: boolean
  /** If true (and selectable), multi-select via checkboxes; else single-select via radio. */
  multiple?: boolean
  onSelect?: (selected: T[]) => void
  rowActions?: JournalAction<T>[]
  customButtons?: JournalCustomButton<T>[]
  /** When provided, shows a "+ Create" button in the footer. */
  onCreate?: () => void
  /** When true, auto-closes when the table becomes empty (Kotlin `closeWhenAllRecordsDeleted`). */
  closeWhenEmpty?: boolean
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
  onSelect,
  rowActions,
  customButtons,
  onCreate,
  closeWhenEmpty = false,
}: JournalDialogProps<T>) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [buttonLoading, setButtonLoading] = useState<string | null>(null)

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    else if (!open && dialog.open) dialog.close()
  }, [open])

  // Reset selection + search when data identity changes (e.g. caller refreshes).
  const [prevData, setPrevData] = useState(data)
  if (data !== prevData) {
    setPrevData(data)
    setSelected(new Set())
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
      className="z-50 fixed rounded-lg border border-border bg-card p-0 text-foreground backdrop:bg-black/50"
      style={{
        width: 'min(90vw, 768px)',
        maxHeight: '80vh',
        top: '50%',
        left: '50%',
        transform: 'translate(-50%, -50%)',
        margin: 0,
      }}
      onClose={onClose}
    >
      <div className="flex flex-col max-h-[80vh]" style={{ width: '100%' }}>
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

        {/* Table */}
        <div className="flex-1 overflow-auto px-4">
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
                          type={multiple ? 'checkbox' : 'radio'}
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
                          const hover = act.variant === 'danger' ? 'hover:text-destructive' : 'hover:text-foreground'
                          return (
                            <button
                              key={idx}
                              type="button"
                              className={`relative inline-flex items-center justify-center p-1 rounded text-muted-foreground ${hover} hover:bg-muted disabled:opacity-40 disabled:hover:bg-transparent`}
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
                    className="py-4 text-center text-muted-foreground"
                  >
                    {search ? t('journal.noMatchingRows') : t('journal.noData')}
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
              const variant =
                btn.variant === 'primary' ? 'bg-primary text-primary-foreground hover:bg-primary/90'
                : btn.variant === 'danger' ? 'border border-destructive/40 text-destructive hover:bg-destructive/10'
                : 'border border-border hover:bg-muted'
              return (
                <button
                  key={btn.label}
                  type="button"
                  className={`rounded-md px-3 py-1.5 text-xs disabled:opacity-50 ${variant}`}
                  disabled={!enabled || buttonLoading === btn.label}
                  onClick={() => runCustomButton(btn)}
                >
                  {buttonLoading === btn.label ? `${btn.label}…` : btn.label}
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
