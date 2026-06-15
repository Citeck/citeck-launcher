import { useNavigate } from 'react-router'
import { SecretsDialog } from '../components/SecretsDialog'

/**
 * /secrets route — thin wrapper around SecretsDialog so the URL keeps
 * resolving for power users / old bookmarks.
 *
 * The dialog is the single source of truth for secret CRUD: it carries the
 * username field (BASIC_AUTH / REGISTRY_AUTH), the scope field (how the
 * daemon binds a secret to a repo / registry) and the ENCRYPTION_NOT_SET_UP →
 * MasterPasswordDialog recovery flow. The previous standalone page was a
 * divergent re-implementation missing all three.
 */
export function Secrets() {
  const navigate = useNavigate()
  return <SecretsDialog open onClose={() => navigate('/')} />
}
