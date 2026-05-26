import { useEffect, useState, useMemo } from 'react'
import { FormDialog, type FormFieldSpec, type FormValues, type SelectOption } from './FormDialog'
import { getBundles, getNamespaceEdit, putNamespaceEdit, type NamespaceEditDto } from '../lib/api'
import type { BundleInfoDto } from '../lib/types'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'
import { useDashboardStore } from '../lib/store'

interface NamespaceEditDialogProps {
  open: boolean
  onClose: () => void
}

/**
 * NamespaceEditDialog is the Web port of Kotlin's EditNamespaceDialog —
 * a typed FormDialog over the namespace.yml fields most users actually want
 * to change. Power users can still edit raw YAML via the RMB "Edit raw YAML"
 * affordance on the Dashboard gear icon.
 *
 * The list of bundle repos is fetched once when the dialog opens; if a repo
 * is not in the dropdown (e.g. the namespace was originally created against
 * a repo that was later removed from workspace-v1.yml), we keep the current
 * value as the default and append a synthetic option so the form does not
 * silently drop it.
 */
export function NamespaceEditDialog({ open, onClose }: NamespaceEditDialogProps) {
  const { t } = useTranslation()
  const [initial, setInitial] = useState<NamespaceEditDto | null>(null)
  const [bundles, setBundles] = useState<BundleInfoDto[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const fetchData = useDashboardStore((s) => s.fetchData)

  useEffect(() => {
    if (!open) {
      setInitial(null)
      setError(null)
      return
    }
    let active = true
    Promise.all([
      getNamespaceEdit().catch((e) => { throw e }),
      getBundles().catch(() => [] as BundleInfoDto[]),
    ])
      .then(([n, b]) => {
        if (!active) return
        setInitial(n)
        setBundles(b)
      })
      .catch((e) => { if (active) setError((e as Error).message) })
    return () => { active = false }
  }, [open])

  const bundleOptions: SelectOption[] = useMemo(() => {
    const opts: SelectOption[] = bundles.map((b) => ({ label: b.repo, value: b.repo }))
    if (initial?.bundleRepo && !opts.some((o) => o.value === initial.bundleRepo)) {
      opts.unshift({ label: initial.bundleRepo, value: initial.bundleRepo })
    }
    return opts
  }, [bundles, initial?.bundleRepo])

  const fields: FormFieldSpec[] = useMemo(() => [
    {
      key: 'name',
      label: t('nsEdit.field.name'),
      type: 'text',
      required: true,
    },
    {
      key: 'bundleRepo',
      label: t('nsEdit.field.bundleRepo'),
      type: 'select',
      required: true,
      options: bundleOptions,
    },
    {
      key: 'bundleKey',
      label: t('nsEdit.field.bundleKey'),
      type: 'text',
      required: true,
      defaultValue: 'LATEST',
    },
    {
      key: 'authType',
      label: t('nsEdit.field.authType'),
      type: 'select',
      required: true,
      options: [
        { label: 'BASIC', value: 'BASIC' },
        { label: 'KEYCLOAK', value: 'KEYCLOAK' },
      ],
    },
    {
      key: 'users',
      label: t('nsEdit.field.users'),
      type: 'text',
      placeholder: 'admin, user1, user2',
    },
    {
      key: 'host',
      label: t('nsEdit.field.host'),
      type: 'text',
      required: true,
    },
    {
      key: 'port',
      label: t('nsEdit.field.port'),
      type: 'number',
      required: true,
      validations: [
        (_, v) => {
          const n = typeof v === 'number' ? v : parseInt(String(v ?? 0), 10) || 0
          return n >= 1 && n <= 65535 ? '' : 'Port must be 1-65535'
        },
      ],
    },
    {
      key: 'tlsEnabled',
      label: t('nsEdit.field.tlsEnabled'),
      type: 'checkbox',
    },
    {
      key: 'pgAdminEnabled',
      label: t('nsEdit.field.pgAdminEnabled'),
      type: 'checkbox',
    },
  ], [t, bundleOptions])

  // Convert the typed DTO into FormValues. Users array is collapsed to a
  // comma-separated string for the text input; submit splits it back.
  const initialValues: FormValues | undefined = useMemo(() => {
    if (!initial) return undefined
    return {
      name: initial.name,
      bundleRepo: initial.bundleRepo,
      bundleKey: initial.bundleKey || 'LATEST',
      authType: initial.authType || 'BASIC',
      users: (initial.users ?? []).join(', '),
      host: initial.host,
      port: initial.port,
      tlsEnabled: initial.tlsEnabled,
      pgAdminEnabled: initial.pgAdminEnabled,
    }
  }, [initial])

  async function handleSubmit(values: FormValues) {
    setLoading(true)
    setError(null)
    try {
      const users = String(values.users ?? '')
        .split(',')
        .map((u) => u.trim())
        .filter(Boolean)
      const payload: NamespaceEditDto = {
        name: String(values.name ?? ''),
        bundleRepo: String(values.bundleRepo ?? ''),
        bundleKey: String(values.bundleKey ?? 'LATEST'),
        authType: String(values.authType ?? 'BASIC'),
        users,
        host: String(values.host ?? ''),
        port: typeof values.port === 'number' ? values.port : parseInt(String(values.port ?? 0), 10) || 0,
        tlsEnabled: Boolean(values.tlsEnabled),
        pgAdminEnabled: Boolean(values.pgAdminEnabled),
      }
      await putNamespaceEdit(payload)
      toast(t('nsEdit.saveSuccess'), 'success')
      onClose()
      fetchData()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <FormDialog
      open={open}
      title={t('nsEdit.title')}
      fields={fields}
      initialValues={initialValues}
      onSubmit={handleSubmit}
      onCancel={onClose}
      loading={loading}
      error={error}
      submitLabel={t('nsEdit.save')}
    />
  )
}
