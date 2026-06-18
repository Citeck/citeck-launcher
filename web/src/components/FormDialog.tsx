import { useEffect, useRef, useState, useMemo } from 'react'
import { Eye, EyeOff, RefreshCw } from 'lucide-react'
import { useTranslation } from '../lib/i18n'
import { Select } from './Select'
import { LoadingLabel } from './LoadingLabel'

/**
 * Form values keyed by field key. Everything goes through this typed shape so
 * `visibleWhen` / `enableWhen` / `validations` don't need to cast each time.
 */
export type FormValues = Record<string, unknown>

/** Validation result: empty string means valid, non-empty is the error message. */
export type ValidationFn = (ctx: FormValues, value: unknown) => string

/** A select option, mirrored from Kotlin's `SelectOption`. */
export interface SelectOption {
  label: string
  value: string
}

/**
 * FormFieldSpec mirrors Kotlin's `ComponentSpec.Field` hierarchy.
 *
 * - `visibleWhen` / `enabledWhen`: predicates over the entire form context;
 *   re-evaluated whenever any depended-on field changes.
 * - `dependsOn`: list of field keys whose changes trigger re-evaluation of
 *   `visibleWhen` / `enabledWhen` / `options`. Mirrors the Kotlin
 *   `dependsOn: MutableSet<String>` set.
 * - `validations`: array of fns; empty string from a fn means valid.
 * - `options`: for select fields, can be a static list OR a function of the
 *   form context (so options can react to other fields).
 * - `onManualUpdate`: optional async callback that fires when the user clicks
 *   the refresh icon next to a select. Used in Kotlin's bundles repo dropdown.
 */
export interface FormFieldSpec {
  key: string
  label: string
  /** Reactive label that depends on other field values (e.g. a value field
   *  labelled "Token" or "Password" by the selected secret type). Falls back
   *  to `label` when absent. Add the driving keys to `dependsOn`. */
  labelWhen?: (ctx: FormValues) => string
  type: 'text' | 'number' | 'password' | 'select' | 'checkbox' | 'display' | 'textarea'
  placeholder?: string
  defaultValue?: unknown
  required?: boolean
  /** Static visibility flag. If false, field is never rendered. */
  visible?: boolean
  /** Reactive visibility. Returns false to hide. */
  visibleWhen?: (ctx: FormValues) => boolean
  /** Reactive enable. Returns false to render as disabled. */
  enabledWhen?: (ctx: FormValues) => boolean
  /** Fields whose mutation triggers reactive re-evaluation of this field. */
  dependsOn?: string[]
  /** Custom validators. Non-empty string is the error message shown on submit. */
  validations?: ValidationFn[]
  /** Select options — static or computed from current form context. */
  options?: SelectOption[] | ((ctx: FormValues) => SelectOption[])
  /** Optional async refresh hook for select fields (renders a refresh icon). */
  onManualUpdate?: (ctx: FormValues) => Promise<void> | void
}

interface FormDialogProps {
  title: string
  fields: FormFieldSpec[]
  onSubmit: (data: FormValues) => void | Promise<void>
  onCancel: () => void
  open: boolean
  loading?: boolean
  error?: string | null
  /** Submit button label. Defaults to common.submit. */
  submitLabel?: string
  /** Initial values; merged over each field's defaultValue. */
  initialValues?: FormValues
}

/** Resolve a field's display label, honouring a reactive `labelWhen`. */
function fieldLabel(field: FormFieldSpec, values: FormValues): string {
  return field.labelWhen ? field.labelWhen(values) : field.label
}

/**
 * FormDialog is the Web port of Kotlin's FormDialog (view/form/FormDialog.kt).
 *
 * Key behaviours preserved from Kotlin:
 * - Submit validation collects ALL errors and shows them as a summary near
 *   the offending field (Kotlin uses MessageDialog; we render inline because
 *   nested modal-stacking in the browser is fragile).
 * - Hidden mandatory fields can still block submit (matches Kotlin's
 *   `FormContext.getInvalidFields` which doesn't check visibility). Form
 *   authors are expected to give hidden mandatory fields valid defaults.
 * - Reactive options + visibility re-evaluate on each render whenever a
 *   field listed in `dependsOn` changes.
 */
export function FormDialog({
  title,
  fields,
  onSubmit,
  onCancel,
  open,
  loading = false,
  error,
  submitLabel,
  initialValues,
}: FormDialogProps) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [values, setValues] = useState<FormValues>({})
  const [validationErrors, setValidationErrors] = useState<Record<string, string>>({})
  const [revealedPwds, setRevealedPwds] = useState<Set<string>>(new Set())
  const [refreshing, setRefreshing] = useState<Set<string>>(new Set())

  // Reset values when dialog opens or the fields spec changes — the "adjust
  // state during render" pattern keeps controlled inputs in sync with their
  // declarative source of truth (matches Kotlin's FormDialog.kt:175 init).
  const [prevOpen, setPrevOpen] = useState(open)
  const [prevFields, setPrevFields] = useState(fields)
  const [prevInitial, setPrevInitial] = useState(initialValues)
  if (open && (open !== prevOpen || fields !== prevFields || initialValues !== prevInitial)) {
    setPrevOpen(open)
    setPrevFields(fields)
    setPrevInitial(initialValues)
    const next: FormValues = {}
    for (const f of fields) {
      if (initialValues && f.key in initialValues) {
        next[f.key] = initialValues[f.key]
      } else if (f.defaultValue !== undefined) {
        next[f.key] = f.defaultValue
      } else if (f.type === 'checkbox') {
        next[f.key] = false
      } else if (f.type === 'number') {
        next[f.key] = 0
      } else {
        next[f.key] = ''
      }
    }
    setValues(next)
    setValidationErrors({})
    setRevealedPwds(new Set())
  }
  if (!open && open !== prevOpen) {
    setPrevOpen(open)
  }

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    else if (!open && dialog.open) dialog.close()
  }, [open])

  function setValue(key: string, value: unknown) {
    setValues((prev) => ({ ...prev, [key]: value }))
    setValidationErrors((prev) => {
      const next = { ...prev }
      delete next[key]
      return next
    })
  }

  /** Visible fields after applying both static `visible` and reactive `visibleWhen`. */
  const visibleFields = useMemo(
    () => fields.filter((f) => f.visible !== false && (!f.visibleWhen || f.visibleWhen(values))),
    [fields, values],
  )

  function resolveOptions(field: FormFieldSpec): SelectOption[] {
    if (typeof field.options === 'function') return field.options(values)
    return field.options ?? []
  }

  function fieldIsEnabled(field: FormFieldSpec): boolean {
    if (loading) return false
    if (!field.enabledWhen) return true
    return field.enabledWhen(values)
  }

  function togglePasswordReveal(key: string) {
    setRevealedPwds((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  async function triggerManualUpdate(field: FormFieldSpec) {
    if (!field.onManualUpdate) return
    setRefreshing((prev) => new Set(prev).add(field.key))
    try {
      await field.onManualUpdate(values)
    } finally {
      setRefreshing((prev) => {
        const next = new Set(prev)
        next.delete(field.key)
        return next
      })
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const errors: Record<string, string> = {}
    // Validate ALL fields including hidden ones — Kotlin parity:
    // FormContext.getInvalidFields walks every field regardless of visibility.
    for (const field of fields) {
      if (field.type === 'display') continue
      const val = values[field.key]
      if (field.required) {
        const empty = val === undefined || val === null || val === ''
        if (empty) {
          errors[field.key] = t('form.fieldRequired', { label: fieldLabel(field, values) })
          continue
        }
      }
      if (field.validations) {
        for (const fn of field.validations) {
          const msg = fn(values, val)
          if (msg) {
            errors[field.key] = msg
            break
          }
        }
      }
    }
    if (Object.keys(errors).length > 0) {
      setValidationErrors(errors)
      return
    }
    await onSubmit(values)
  }

  return (
    <dialog
      ref={dialogRef}
      className="fixed inset-0 z-50 m-auto max-w-lg w-full rounded-lg border border-border bg-card p-0 text-foreground shadow-xl"
      // Guard against a nested <dialog>'s `close` event bubbling up the React
      // fiber tree to this one (it would cancel a dialog opened beneath this
      // form). Only this dialog's own close counts. See Modal.tsx.
      onClose={(e) => { if (e.target === e.currentTarget) onCancel() }}
    >
      <form onSubmit={handleSubmit} className="p-6">
        <h2 className="text-lg font-semibold mb-4">{title}</h2>

        <div className="space-y-3">
          {visibleFields.map((field) => {
            const enabled = fieldIsEnabled(field)
            return (
              <div key={field.key}>
                <label className="block text-xs font-medium text-muted-foreground mb-0.5">
                  {fieldLabel(field, values)}
                  {field.required && <span className="text-destructive ml-0.5">*</span>}
                </label>

                {field.type === 'text' && (
                  <input
                    type="text"
                    className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary disabled:opacity-50"
                    value={(values[field.key] as string) ?? ''}
                    onChange={(e) => setValue(field.key, e.target.value)}
                    placeholder={field.placeholder}
                    disabled={!enabled}
                  />
                )}

                {field.type === 'textarea' && (
                  <textarea
                    className="w-full h-32 rounded border border-border bg-background px-2.5 py-1.5 text-sm font-mono focus:outline-none focus:border-primary disabled:opacity-50"
                    value={(values[field.key] as string) ?? ''}
                    onChange={(e) => setValue(field.key, e.target.value)}
                    placeholder={field.placeholder}
                    disabled={!enabled}
                  />
                )}

                {field.type === 'password' && (
                  <div className="relative">
                    <input
                      type={revealedPwds.has(field.key) ? 'text' : 'password'}
                      className="w-full rounded border border-border bg-background px-2.5 py-1.5 pr-8 text-sm focus:outline-none focus:border-primary disabled:opacity-50"
                      value={(values[field.key] as string) ?? ''}
                      onChange={(e) => setValue(field.key, e.target.value)}
                      placeholder={field.placeholder}
                      disabled={!enabled}
                    />
                    <button
                      type="button"
                      className="absolute right-1 top-1/2 -translate-y-1/2 p-1 text-muted-foreground hover:text-foreground"
                      onClick={() => togglePasswordReveal(field.key)}
                      tabIndex={-1}
                      title={revealedPwds.has(field.key) ? t('form.hidePassword') : t('form.showPassword')}
                    >
                      {revealedPwds.has(field.key) ? <EyeOff size={14} /> : <Eye size={14} />}
                    </button>
                  </div>
                )}

                {field.type === 'number' && (
                  <input
                    type="number"
                    className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary disabled:opacity-50"
                    value={(values[field.key] as number) ?? 0}
                    onChange={(e) => setValue(field.key, parseInt(e.target.value, 10) || 0)}
                    placeholder={field.placeholder}
                    disabled={!enabled}
                  />
                )}

                {field.type === 'select' && (
                  <div className="flex items-center gap-1">
                    <Select
                      className="flex-1"
                      value={(values[field.key] as string) ?? ''}
                      options={resolveOptions(field)}
                      onChange={(v) => setValue(field.key, v)}
                      disabled={!enabled}
                      required={field.required}
                      placeholder={field.placeholder}
                    />
                    {field.onManualUpdate && (
                      <button
                        type="button"
                        className="rounded border border-border p-1.5 text-muted-foreground hover:text-foreground hover:bg-muted disabled:opacity-50"
                        onClick={() => triggerManualUpdate(field)}
                        disabled={!enabled || refreshing.has(field.key)}
                        title={t('form.refreshOptions')}
                      >
                        <RefreshCw size={14} className={refreshing.has(field.key) ? 'animate-spin' : ''} />
                      </button>
                    )}
                  </div>
                )}

                {field.type === 'checkbox' && (
                  <label className="flex items-center gap-2 text-sm cursor-pointer">
                    <input
                      type="checkbox"
                      className="rounded border-border"
                      checked={(values[field.key] as boolean) ?? false}
                      onChange={(e) => setValue(field.key, e.target.checked)}
                      disabled={!enabled}
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
            )
          })}
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
            {t('common.cancel')}
          </button>
          <button
            type="submit"
            className="rounded-md bg-primary text-primary-foreground px-4 py-2 text-sm font-medium hover:bg-primary/90 disabled:opacity-50"
            disabled={loading}
          >
            <LoadingLabel loading={loading}>{submitLabel ?? t('common.submit')}</LoadingLabel>
          </button>
        </div>
      </form>
    </dialog>
  )
}
