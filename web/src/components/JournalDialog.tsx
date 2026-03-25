import { useEffect, useRef, useState, useMemo } from 'react'
import { Search, X } from 'lucide-react'

export interface JournalColumn {
  label: string
  key: string
  width?: string
}

interface JournalDialogProps {
  title: string
  columns: JournalColumn[]
  data: Record<string, unknown>[]
  selectable?: boolean
  multiple?: boolean
  onSelect?: (selected: Record<string, unknown>[]) => void
  customButtons?: { label: string; onClick: (selected: Record<string, unknown>[]) => void }[]
  onClose: () => void
  open: boolean
}

export function JournalDialog({
  title,
  columns,
  data,
  selectable = false,
  multiple = false,
  onSelect,
  customButtons,
  onClose,
  open,
}: JournalDialogProps) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<Set<string>>(new Set())

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (open && !dialog.open) {
      dialog.showModal()
    } else if (!open && dialog.open) {
      dialog.close()
    }
  }, [open])

  // Reset selection when data changes
  useEffect(() => {
    setSelected(new Set())
  }, [data])

  // Stable row identity using the first column value
  function rowKey(row: Record<string, unknown>): string {
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

  function toggleRow(row: Record<string, unknown>) {
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

  function getSelectedRows(): Record<string, unknown>[] {
    return filteredData.filter((row) => selected.has(rowKey(row)))
  }

  function handleSelect() {
    if (onSelect) {
      onSelect(getSelectedRows())
    }
    onClose()
  }

  return (
    <dialog
      ref={dialogRef}
      className="fixed inset-0 z-50 m-auto max-w-3xl w-full max-h-[80vh] rounded-lg border border-border bg-card p-0 text-foreground backdrop:bg-black/50"
      onClose={onClose}
    >
      <div className="flex flex-col h-full max-h-[80vh]">
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-border shrink-0">
          <h2 className="text-sm font-semibold">{title}</h2>
          <button
            type="button"
            className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted"
            onClick={onClose}
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
              placeholder="Filter..."
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
                >
                  {selectable && (
                    <td className="py-[3px] pr-2">
                      <input
                        type={multiple ? 'checkbox' : 'radio'}
                        className="rounded border-border"
                        checked={isSelected}
                        onChange={() => toggleRow(row)}
                      />
                    </td>
                  )}
                  {columns.map((col) => (
                    <td key={col.key} className="py-[3px] pr-4 text-muted-foreground">
                      {row[col.key] != null ? String(row[col.key]) : ''}
                    </td>
                  ))}
                </tr>
              )})}
              {filteredData.length === 0 && (
                <tr>
                  <td
                    colSpan={columns.length + (selectable ? 1 : 0)}
                    className="py-4 text-center text-muted-foreground"
                  >
                    {search ? 'No matching rows' : 'No data'}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between px-4 py-3 border-t border-border shrink-0">
          <span className="text-[11px] text-muted-foreground">
            {filteredData.length} row{filteredData.length !== 1 ? 's' : ''}
            {selectable && selected.size > 0 && ` (${selected.size} selected)`}
          </span>
          <div className="flex items-center gap-2">
            {customButtons?.map((btn) => (
              <button
                key={btn.label}
                type="button"
                className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted"
                onClick={() => btn.onClick(getSelectedRows())}
              >
                {btn.label}
              </button>
            ))}
            {selectable && onSelect && (
              <button
                type="button"
                className="rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-xs font-medium hover:bg-primary/90 disabled:opacity-50"
                onClick={handleSelect}
                disabled={selected.size === 0}
              >
                Select
              </button>
            )}
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted"
              onClick={onClose}
            >
              Close
            </button>
          </div>
        </div>
      </div>
    </dialog>
  )
}
