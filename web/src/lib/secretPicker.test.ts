import { describe, it, expect } from 'vitest'
import {
  slugFromName,
  generateSecretId,
  buildGitTokenCreate,
  buildRegistrySecretCreate,
  workspaceSecretInUse,
  needsWorkspaceRelink,
  workspacesUsingSecret,
} from './secretPicker'
import { extractHost, isAuthShapedGitError } from './giturl'

describe('slugFromName', () => {
  const cases: [string, string][] = [
    ['GitLab', 'gitlab'],
    ['  My  Company Token ', 'my-company-token'],
    ['gitlab.example.com', 'gitlab-example-com'],
    ['x___y--z', 'x-y-z'],
    ['---', 'token'], // nothing usable left → fallback
    ['Токен', 'token'], // non-latin strips entirely → fallback
    ['', 'token'],
  ]
  it.each(cases)('slugifies %j → %j', (input, expected) => {
    expect(slugFromName(input)).toBe(expected)
  })
})

describe('generateSecretId', () => {
  it('prefixes git-token- and uses the slug', () => {
    expect(generateSecretId('GitLab', [])).toBe('git-token-gitlab')
  })

  it('appends a numeric suffix on collision', () => {
    expect(generateSecretId('GitLab', ['git-token-gitlab'])).toBe('git-token-gitlab-2')
    expect(generateSecretId('GitLab', ['git-token-gitlab', 'git-token-gitlab-2'])).toBe('git-token-gitlab-3')
  })

  it('ignores unrelated existing ids', () => {
    expect(generateSecretId('GitLab', ['git-token-github', 'other'])).toBe('git-token-gitlab')
  })
})

describe('extractHost (default secret name source)', () => {
  const cases: [string, string][] = [
    ['https://gitlab.example.com/group/repo.git', 'gitlab.example.com'],
    ['git@github.com:org/repo.git', 'github.com'],
    ['HTTPS://GitLab.COM/x.git', 'gitlab.com'],
    ['not a url', ''],
    ['', ''],
  ]
  it.each(cases)('extracts %j → %j', (input, expected) => {
    expect(extractHost(input)).toBe(expected)
  })
})

describe('buildGitTokenCreate', () => {
  it('maps fields and generates a git-token- id', () => {
    expect(buildGitTokenCreate(' GitLab ', 'tok', [])).toEqual({
      id: 'git-token-gitlab',
      name: 'GitLab',
      type: 'GIT_TOKEN',
      value: 'tok',
    })
  })

  it('returns null when name or token is missing', () => {
    expect(buildGitTokenCreate('', 'tok', [])).toBeNull()
    expect(buildGitTokenCreate('GitLab', '', [])).toBeNull()
  })
})

describe('buildRegistrySecretCreate', () => {
  it('tags the payload with the host and uses the registry- prefix', () => {
    // The host tag is the model contract between the picker and the daemon —
    // without it the host-filtered picker never surfaces the new credential.
    expect(
      buildRegistrySecretCreate('Harbor', ' user ', 'pass', 'harbor.citeck.ru', []),
    ).toEqual({
      id: 'registry-harbor',
      name: 'Harbor',
      type: 'REGISTRY_AUTH',
      username: 'user',
      value: 'pass',
      host: 'harbor.citeck.ru',
    })
  })

  it('suffixes the id on collision', () => {
    expect(
      buildRegistrySecretCreate('Harbor', 'u', 'p', 'h', ['registry-harbor'])?.id,
    ).toBe('registry-harbor-2')
  })

  it('returns null when name, username or password is missing', () => {
    expect(buildRegistrySecretCreate('', 'u', 'p', 'h', [])).toBeNull()
    expect(buildRegistrySecretCreate('n', '', 'p', 'h', [])).toBeNull()
    expect(buildRegistrySecretCreate('n', 'u', '', 'h', [])).toBeNull()
  })
})

describe('workspaceSecretInUse', () => {
  it('prefers the explicit link, falls back to the legacy ws:<id>:repo id', () => {
    expect(workspaceSecretInUse({ id: 'w1', authType: 'TOKEN', secretId: 's1' })).toBe('s1')
    expect(workspaceSecretInUse({ id: 'w1', authType: 'TOKEN', secretId: '' })).toBe('ws:w1:repo')
    expect(workspaceSecretInUse({ id: 'w1', authType: 'NONE', secretId: '' })).toBe('')
    expect(workspaceSecretInUse(undefined)).toBe('')
  })
})

describe('needsWorkspaceRelink', () => {
  it('relinks only when a different non-empty secret is picked', () => {
    expect(needsWorkspaceRelink('s1', 's2')).toBe(true)
    expect(needsWorkspaceRelink('s1', 's1')).toBe(false)
    expect(needsWorkspaceRelink('s1', '')).toBe(false)
  })
})

describe('workspacesUsingSecret', () => {
  const wss = [
    { id: 'w1', name: 'Alpha', secretId: 's1' },
    { id: 'w2', name: '', secretId: 's1' },
    { id: 'w3', name: 'Gamma', secretId: 's2' },
  ]
  it('returns names (falling back to id) of workspaces linked by secretId only', () => {
    expect(workspacesUsingSecret('s1', wss)).toEqual(['Alpha', 'w2'])
    expect(workspacesUsingSecret('s2', wss)).toEqual(['Gamma'])
    expect(workspacesUsingSecret('', wss)).toEqual([])
  })
})

describe('isAuthShapedGitError', () => {
  it('matches go-git auth failures', () => {
    expect(isAuthShapedGitError('clone repo https://x/y.git: authentication required')).toBe(true)
    expect(isAuthShapedGitError('pull repo: authorization failed')).toBe(true)
    expect(isAuthShapedGitError('unexpected client error: 403 Forbidden')).toBe(true)
  })

  it('rejects non-auth git errors', () => {
    expect(isAuthShapedGitError('repository not found')).toBe(false)
    expect(isAuthShapedGitError('dial tcp: connection refused')).toBe(false)
    expect(isAuthShapedGitError('')).toBe(false)
    expect(isAuthShapedGitError(null)).toBe(false)
  })
})
