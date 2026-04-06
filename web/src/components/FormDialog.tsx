import { useEffect, useRef, useState } from 'react'

export interface FormFieldSpec {
  key: string
  label: string
  type: 'text' | 'number' | 'password' | 'select' | 'checkbox' | 'display'
  required?: boolean
  placeholder?: string
  options?: { label: string; value: string }[]
  visible?: boolean
  defaultValue?: string | number | boolean
}

interface FormDialogProps {
  title: string
  fields: FormFieldSpec[]
  onSubmit: (data: Record<string, unknown>) => void
  onCancel: () => void
  open: boolean
  loading?: boolean
  error?: string | null
}

export function FormDialog({ title, fields, onSubmit, onCancel, open, loading = false, error }: FormDialogProps) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [values, setValues] = useState<Record<string, unknown>>({})
  const [validationErrors, setValidationErrors] = useState<Record<string, string>>({})

  // Reset values when dialog opens or fields change — "adjust state during render" pattern
  const [prevOpen, setPrevOpen] = useState(open)
  const [prevFields, setPrevFields] = useState(fields)
  if (open && (open !== prevOpen || fields !== prevFields)) {
    setPrevOpen(open)
    setPrevFields(fields)
    const defaults: Record<string, unknown> = {}
    for (const field of fields) {
      if (field.defaultValue !== undefined) {
        defaults[field.key] = field.defaultValue
      } else if (field.type === 'checkbox') {
        defaults[field.key] = false
      } else if (field.type === 'number') {
        defaults[field.key] = 0
      } else {
        defaults[field.key] = ''
      }
    }
    setValues(defaults)
    setValidationErrors({})
  }
  if (!open && open !== prevOpen) {
    setPrevOpen(open)
  }

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (open && !dialog.open) {
      dialog.showModal()
    } else if (!open && dialog.open) {
      dialog.close()
    }
  }, [open])

  function setValue(key: string, value: unknown) {
    setValues((prev) => ({ ...prev, [key]: value }))
    setValidationErrors((prev) => {
      const next = { ...prev }
      delete next[key]
      return next
    })
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const errors: Record<string, string> = {}
    for (const field of visibleFields) {
      if (field.required && field.type !== 'display') {
        const val = values[field.key]
        if (val === undefined || val === null || val === '') {
          errors[field.key] = `${field.label} is required`
        }
      }
    }
    if (Object.keys(errors).length > 0) {
      setValidationErrors(errors)
      return
    }
    onSubmit(values)
  }

  const visibleFields = fields.filter((f) => f.visible !== false)

  return (
    <dialog
      ref={dialogRef}
      className="fixed inset-0 z-50 m-auto max-w-md w-full rounded-lg border border-border bg-card p-0 text-foreground backdrop:bg-black/50"
      onClose={onCancel}
    >
      <form onSubmit={handleSubmit} className="p-6">
        <h2 className="text-lg font-semibold mb-4">{title}</h2>

        <div className="space-y-3">
          {visibleFields.map((field) => (
            <div key={field.key}>
              <label className="block text-xs font-medium text-muted-foreground mb-0.5">
                {field.label}
                {field.required && <span className="text-destructive ml-0.5">*</span>}
              </label>

              {field.type === 'text' && (
                <input
                  type="text"
                  className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
                  value={(values[field.key] as string) ?? ''}
                  onChange={(e) => setValue(field.key, e.target.value)}
                  placeholder={field.placeholder}
                />
              )}

              {field.type === 'password' && (
                <input
                  type="password"
                  className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
                  value={(values[field.key] as string) ?? ''}
                  onChange={(e) => setValue(field.key, e.target.value)}
                  placeholder={field.placeholder}
                />
              )}

              {field.type === 'number' && (
                <input
                  type="number"
                  className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
                  value={(values[field.key] as number) ?? 0}
                  onChange={(e) => setValue(field.key, parseInt(e.target.value, 10) || 0)}
                  placeholder={field.placeholder}
                />
              )}

              {field.type === 'select' && (
                <select
                  className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
                  value={(values[field.key] as string) ?? ''}
                  onChange={(e) => setValue(field.key, e.target.value)}
                >
                  {field.options?.map((opt) => (
                    <option key={opt.value} value={opt.value}>{opt.label}</option>
                  ))}
                </select>
              )}

              {field.type === 'checkbox' && (
                <label className="flex items-center gap-2 text-sm cursor-pointer">
                  <input
                    type="checkbox"
                    className="rounded border-border"
                    checked={(values[field.key] as boolean) ?? false}
                    onChange={(e) => setValue(field.key, e.target.checked)}
                  />
                  {field.placeholder ?? field.label}
                </label>
              )}

              {field.type === 'display' && (
                <div className="text-sm text-muted-foreground py-1">
                  {(values[field.key] as string) ?? field.defaultValue ?? ''}
                </div>
              )}

              {validationErrors[field.key] && (
                <div className="text-destructive text-[11px] mt-0.5">{validationErrors[field.key]}</div>
              )}
            </div>
          ))}
        </div>

        {error && (
          <p className="mt-3 rounded-md bg-destructive/10 border border-destructive/20 px-3 py-2 text-sm text-destructive">
            {error}
          </p>
        )}

        <div className="mt-6 flex justify-end gap-3">
          <button
            type="button"
            className="rounded-md border border-border px-4 py-2 text-sm hover:bg-muted"
            onClick={onCancel}
            disabled={loading}
          >
            Cancel
          </button>
          <button
            type="submit"
            className="rounded-md bg-primary text-primary-foreground px-4 py-2 text-sm font-medium hover:bg-primary/90 disabled:opacity-50"
            disabled={loading}
          >
            {loading ? 'Working...' : 'Submit'}
          </button>
        </div>
      </form>
    </dialog>
  )
}
