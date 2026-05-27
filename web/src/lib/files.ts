// Mirrors Kotlin `EDITABLE_FILE_EXTENSIONS` (NamespaceScreen.kt v1.3.8).
// Shared so the COG RMB menu and the AppConfigEditor inline list filter the
// same way — otherwise binary mounts (certs, fonts, jars) show as "Edit"
// targets and explode the textual editor with garbage.
export const EDITABLE_FILE_EXTENSIONS = ['yaml', 'yml', 'json', 'kt', 'java', 'js', 'lua', 'Dockerfile', 'sh', 'txt', 'conf']

export function isEditableFile(path: string): boolean {
  const base = path.split('/').pop() ?? path
  if (base === 'Dockerfile') return true
  const dot = base.lastIndexOf('.')
  if (dot < 0) return false
  return EDITABLE_FILE_EXTENSIONS.includes(base.slice(dot + 1))
}
