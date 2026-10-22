// The one list of secrets backends. The first-run wizard and Settings both
// read from here, so they always show the same choices with the same names.
import { type ProviderType as GeneratedProviderType } from '@/generated/provider-types'

// The set of accepted type STRINGS is generated from the backend factory in
// internal/providers/provider.go via cmd/gen-provider-types. The generated
// union includes every alias each switch arm accepts (aws-sm /
// aws-secrets-manager, kubernetes / k8s-secrets, ...). The list below has one
// row per real backend, and CANONICAL_TYPE collapses every alias onto its row.

// One canonical value per real backend. Every canonical value is a member of
// the generated union, so anything the form submits is backend-acceptable.
export type CanonicalProviderType = 'argocd' | 'aws-sm' | 'k8s-secrets' | 'azure' | 'gcp'

// '' means "none": no backend chosen.
export type ProviderType = '' | CanonicalProviderType

// A TOTAL record over the generated union: a new arm in the backend factory
// is a compile error here until it is mapped.
const CANONICAL_TYPE: Record<GeneratedProviderType, CanonicalProviderType> = {
  argocd: 'argocd',
  'aws-sm': 'aws-sm',
  'aws-secrets-manager': 'aws-sm',
  azure: 'azure',
  'azure-kv': 'azure',
  'azure-key-vault': 'azure',
  gcp: 'gcp',
  'gcp-sm': 'gcp',
  'google-secret-manager': 'gcp',
  'k8s-secrets': 'k8s-secrets',
  kubernetes: 'k8s-secrets',
}

// Collapses any stored provider type (canonical or alias) onto its row.
// Unknown or empty input maps to '' ("None").
export function canonicalizeProviderType(raw: string | undefined | null): ProviderType {
  if (!raw) return ''
  return (CANONICAL_TYPE as Record<string, CanonicalProviderType>)[raw] ?? ''
}

export interface ProviderOption {
  value: CanonicalProviderType
  label: string
  // Azure Key Vault and Google Secret Manager exist in the backend but are
  // not built yet. They show as disabled instead of looking like they work.
  supported: boolean
}

// Plain names only. No internal ids in brackets.
export const PROVIDER_OPTIONS: readonly ProviderOption[] = [
  { value: 'argocd', label: 'ArgoCD (uses the cluster logins ArgoCD already has)', supported: true },
  { value: 'aws-sm', label: 'AWS Secrets Manager', supported: true },
  { value: 'k8s-secrets', label: 'Kubernetes Secrets', supported: true },
  { value: 'azure', label: 'Azure Key Vault — not yet supported', supported: false },
  { value: 'gcp', label: 'Google Secret Manager — not yet supported', supported: false },
]

// ArgoCD is not valid for addon secret values; the server rejects it.
export const ADDON_SECRET_PROVIDER_OPTIONS: readonly ProviderOption[] = PROVIDER_OPTIONS.filter(
  (o) => o.value !== 'argocd',
)

// The short name of a backend, for badges and the status card. A stored type
// we do not know is shown as it is.
export function backendLabel(raw: string | undefined | null): string {
  const canonical = canonicalizeProviderType(raw)
  if (!canonical) return raw || ''
  const full = PROVIDER_OPTIONS.find((o) => o.value === canonical)?.label ?? raw ?? ''
  return canonical === 'argocd' ? 'ArgoCD' : full.replace(' — not yet supported', '')
}

// Only AWS Secrets Manager uses a region and a name prefix.
export const REGION_PROVIDERS: ReadonlySet<ProviderType> = new Set<ProviderType>(['aws-sm'])
export const PREFIX_PROVIDERS: ReadonlySet<ProviderType> = new Set<ProviderType>(['aws-sm'])

export const SECRETS_PROVIDER_DOC_URL = 'https://sharko.readthedocs.io/en/latest/user-guide/secrets-provider/'
