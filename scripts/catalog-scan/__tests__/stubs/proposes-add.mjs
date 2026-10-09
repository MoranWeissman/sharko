/**
 * Test stub: proposes one new entry that does NOT exist in
 * fixtures/addons.tiny.yaml. Used by the 'dry-run-stdout' and
 * 'default-writes-output' integration cases.
 */
export const name = 'stub-proposes-add';

export async function fetch(_ctx) {
  return [
    {
      name: 'argo-cd',
      chart: 'argo-cd',
      repo: 'https://argoproj.github.io/argo-helm',
      version: '7.6.10',
      category: 'gitops',
      trust_score: 92,
    },
  ];
}
