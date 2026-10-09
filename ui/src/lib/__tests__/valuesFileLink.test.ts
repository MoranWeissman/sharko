import { describe, it, expect } from 'vitest'
import { editOnHostLabel, toRepoFormat, valuesFilePath, valuesFileURL } from '@/lib/valuesFileLink'

// v4.0.4 U9b: the values-file links pointed at the v3 layout on v4 repos.
describe('valuesFileLink', () => {
  const base = 'https://github.com/acme/gitops'

  it('points at the v4 layout on a v4 repo', () => {
    expect(valuesFileURL(base, 'main', 'v4', 'cert-manager')).toBe(
      'https://github.com/acme/gitops/blob/main/values/global/cert-manager.yaml',
    )
    expect(valuesFileURL(base, 'main', 'v4', 'cert-manager', 'spoke-us')).toBe(
      'https://github.com/acme/gitops/blob/main/values/clusters/spoke-us/cert-manager.yaml',
    )
  })

  it('never builds a v3 path for a v4 repo', () => {
    for (const url of [
      valuesFileURL(base, 'main', 'v4', 'cert-manager'),
      valuesFileURL(base, 'main', 'v4', 'cert-manager', 'spoke-us'),
    ]) {
      expect(url).not.toMatch(/configuration\/addons-/)
    }
  })

  it('keeps the v3 layout on a v3 repo', () => {
    expect(valuesFilePath('v3', 'cert-manager')).toBe('configuration/addons-global-values/cert-manager.yaml')
    expect(valuesFilePath('v3', 'cert-manager', 'spoke-us')).toBe('configuration/addons-clusters-values/spoke-us.yaml')
  })

  it('shows no link when the layout or the repo is unknown', () => {
    expect(valuesFileURL(base, 'main', undefined, 'cert-manager')).toBeUndefined()
    expect(valuesFileURL('', 'main', 'v4', 'cert-manager')).toBeUndefined()
    expect(toRepoFormat('')).toBeUndefined()
    expect(toRepoFormat('v4')).toBe('v4')
  })

  it('names the Git host on the link, or stays neutral', () => {
    expect(editOnHostLabel('GitHub')).toBe('Edit in GitHub')
    expect(editOnHostLabel('Gitea')).toBe('Edit in Gitea')
    expect(editOnHostLabel(null)).toBe('Open the file in your Git host')
  })
})
