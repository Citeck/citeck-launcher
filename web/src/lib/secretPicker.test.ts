import { describe, it, expect } from 'vitest'
import { slugFromName, generateSecretId } from './secretPicker'
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
