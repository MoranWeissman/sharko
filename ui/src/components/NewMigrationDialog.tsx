import { useState, useEffect, useCallback } from 'react'
import { Loader2 } from 'lucide-react'
import { api } from '@/services/api'
import type { Migration, ClusterAddonInfo } from '@/services/api'
import { Button } from '@/components/ui/button'
import { SearchableSelect } from '@/components/SearchableSelect'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

type MigrationScope = 'single' | 'multiple' | 'cluster'

interface NewMigrationDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onStarted: (migration: Migration) => void
}

export function NewMigrationDialog({ open, onOpenChange, onStarted }: NewMigrationDialogProps) {
  const [scope, setScope] = useState<MigrationScope>('single')
  const [mode, setMode] = useState<'gates' | 'yolo'>('gates')
  const [clusterName, setClusterName] = useState('')
  const [selectedAddons, setSelectedAddons] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Data from OLD repo
  const [addons, setAddons] = useState<string[]>([])
  const [clusters, setClusters] = useState<string[]>([])
  const [clusterAddons, setClusterAddons] = useState<ClusterAddonInfo[]>([])
  const [loadingAddons, setLoadingAddons] = useState(false)
  const [loadingClusters, setLoadingClusters] = useState(false)
  const [loadingClusterAddons, setLoadingClusterAddons] = useState(false)

  // Fetch addons and clusters when dialog opens
  const fetchData = useCallback(async () => {
    if (!open) return
    setLoadingAddons(true)
    setLoadingClusters(true)
    try {
      const [addonList, clusterList] = await Promise.all([
        api.oldRepoAddons(),
        api.oldRepoClusters(),
      ])
      setAddons(addonList)
      setClusters(clusterList)
    } catch {
      setError('Failed to fetch data from old repo. Check migration settings.')
    } finally {
      setLoadingAddons(false)
      setLoadingClusters(false)
    }
  }, [open])

  useEffect(() => {
    void fetchData()
  }, [fetchData])

  // Fetch enabled addons when cluster is selected (for "cluster" scope)
  const handleClusterChange = async (cluster: string) => {
    setClusterName(cluster)
    setSelectedAddons([])
    setClusterAddons([])
    if (!cluster) return

    if (scope === 'cluster') {
      setLoadingClusterAddons(true)
      try {
        const enabled = await api.oldRepoClusterAddons(cluster)
        setClusterAddons(enabled)
        // Auto-select only non-migrated addons
        setSelectedAddons(enabled.filter(a => !a.already_migrated).map(a => a.name))
      } catch {
        // ignore — user can still proceed
      } finally {
        setLoadingClusterAddons(false)
      }
    }
  }

  // When scope changes to cluster and a cluster is already selected, fetch its addons
  useEffect(() => {
    if (scope === 'cluster' && clusterName) {
      void handleClusterChange(clusterName)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scope])

  const handleAddonToggle = (addon: string) => {
    setSelectedAddons((prev) =>
      prev.includes(addon) ? prev.filter((a) => a !== addon) : [...prev, addon]
    )
  }

  const handleStart = async () => {
    if (!clusterName || selectedAddons.length === 0) return
    setLoading(true)
    setError(null)

    try {
      if (scope === 'cluster' && selectedAddons.length > 1) {
        // Batch mode: sequential queue for all selected addons
        const batch = await api.startBatch({
          addons: selectedAddons,
          cluster_name: clusterName,
          mode: mode,
        })
        // Navigate to the first migration in the batch
        const firstMigration = await api.getMigration(batch.migration_ids[0])
        onStarted(firstMigration)
      } else {
        // Single addon
        const migration = await api.startMigration({
          addon_name: selectedAddons[0],
          cluster_name: clusterName,
          mode: mode,
        })
        onStarted(migration)
      }
      // Reset state
      setScope('single')
      setMode('gates')
      setClusterName('')
      setSelectedAddons([])
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to start migration')
    } finally {
      setLoading(false)
    }
  }

  const canStart = clusterName && selectedAddons.length > 0 && !loading

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>New Migration</DialogTitle>
          <DialogDescription>
            Migrate addons from the old ArgoCD to the new one.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4 py-2">
          {error && (
            <div className="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/30 dark:text-red-400">
              {error}
            </div>
          )}

          {/* Scope selection */}
          <div>
            <label className="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
              What do you want to migrate?
            </label>
            <div className="grid grid-cols-3 gap-2">
              {([
                { value: 'single', label: 'Single Addon' },
                { value: 'multiple', label: 'Multiple Addons' },
                { value: 'cluster', label: 'Entire Cluster' },
              ] as const).map((opt) => (
                <button
                  key={opt.value}
                  type="button"
                  onClick={() => { setScope(opt.value); setSelectedAddons([]) }}
                  className={`rounded-lg border px-3 py-2 text-sm font-medium transition-colors ${
                    scope === opt.value
                      ? 'border-cyan-500 bg-cyan-50 text-cyan-700 dark:border-cyan-400 dark:bg-cyan-900/30 dark:text-cyan-300'
                      : 'border-gray-200 text-gray-600 hover:border-gray-300 dark:border-gray-700 dark:text-gray-400 dark:hover:border-gray-600'
                  }`}
                >
                  {opt.label}
                </button>
              ))}
            </div>
          </div>

          {/* Mode selection */}
          <div>
            <label className="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
              Migration Mode
            </label>
            <div className="grid grid-cols-2 gap-2">
              <button
                type="button"
                onClick={() => setMode('gates')}
                className={`rounded-lg border px-3 py-2 text-left transition-colors ${
                  mode === 'gates'
                    ? 'border-cyan-500 bg-cyan-50 text-cyan-700 dark:border-cyan-400 dark:bg-cyan-900/30 dark:text-cyan-300'
                    : 'border-gray-200 text-gray-600 hover:border-gray-300 dark:border-gray-700 dark:text-gray-400 dark:hover:border-gray-600'
                }`}
              >
                <div className="font-medium">Gates</div>
                <div className="text-xs text-gray-500">Approve each step manually</div>
              </button>
              <button
                type="button"
                onClick={() => setMode('yolo')}
                className={`rounded-lg border px-3 py-2 text-left transition-colors ${
                  mode === 'yolo'
                    ? 'border-cyan-500 bg-cyan-50 text-cyan-700 dark:border-cyan-400 dark:bg-cyan-900/30 dark:text-cyan-300'
                    : 'border-gray-200 text-gray-600 hover:border-gray-300 dark:border-gray-700 dark:text-gray-400 dark:hover:border-gray-600'
                }`}
              >
                <div className="font-medium">YOLO</div>
                <div className="text-xs text-gray-500">Fully automatic</div>
              </button>
            </div>
          </div>

          {/* Cluster selection */}
          <div>
            <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
              Cluster
            </label>
            <SearchableSelect
              options={clusters}
              value={clusterName}
              onChange={handleClusterChange}
              placeholder="Search clusters..."
              loading={loadingClusters}
              disabled={loading}
            />
          </div>

          {/* Addon selection — single */}
          {scope === 'single' && (
            <div>
              <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
                Addon
              </label>
              <SearchableSelect
                options={addons}
                value={selectedAddons[0] ?? ''}
                onChange={(v) => setSelectedAddons(v ? [v] : [])}
                placeholder="Search addons..."
                loading={loadingAddons}
                disabled={loading}
              />
            </div>
          )}

          {/* Addon selection — multiple (checkboxes) */}
          {scope === 'multiple' && (
            <div>
              <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
                Select Addons ({selectedAddons.length} selected)
              </label>
              <div className="max-h-48 overflow-auto rounded-lg border border-gray-200 p-2 dark:border-gray-700">
                {loadingAddons ? (
                  <div className="flex items-center gap-2 p-2 text-sm text-gray-500">
                    <Loader2 className="h-4 w-4 animate-spin" /> Loading...
                  </div>
                ) : addons.length === 0 ? (
                  <p className="p-2 text-sm text-gray-500">No addons found</p>
                ) : (
                  addons.map((addon) => (
                    <label
                      key={addon}
                      className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm hover:bg-gray-50 dark:hover:bg-gray-800"
                    >
                      <input
                        type="checkbox"
                        checked={selectedAddons.includes(addon)}
                        onChange={() => handleAddonToggle(addon)}
                        className="h-4 w-4 rounded border-gray-300"
                        disabled={loading}
                      />
                      <span className="text-gray-700 dark:text-gray-300">{addon}</span>
                    </label>
                  ))
                )}
              </div>
            </div>
          )}

          {/* Addon selection — entire cluster (show what will be migrated) */}
          {scope === 'cluster' && clusterName && (
            <div>
              <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
                Addons on {clusterName} ({clusterAddons.filter(a => !a.already_migrated).length} to migrate, {clusterAddons.filter(a => a.already_migrated).length} already migrated)
              </label>
              {loadingClusterAddons ? (
                <div className="flex items-center gap-2 p-2 text-sm text-gray-500">
                  <Loader2 className="h-4 w-4 animate-spin" /> Loading cluster addons...
                </div>
              ) : clusterAddons.length === 0 ? (
                <p className="rounded-lg border border-gray-200 p-3 text-sm text-gray-500 dark:border-gray-700">
                  No enabled addons found on this cluster
                </p>
              ) : (
                <div className="max-h-48 overflow-auto rounded-lg border border-gray-200 p-2 dark:border-gray-700">
                  {clusterAddons.map((addon) => (
                    <div
                      key={addon.name}
                      className="flex items-center gap-2 rounded px-2 py-1.5 text-sm"
                    >
                      <div className={`h-2 w-2 rounded-full ${addon.already_migrated ? 'bg-blue-500' : 'bg-green-500'}`} />
                      <span className={addon.already_migrated ? 'text-gray-400 dark:text-gray-500' : 'text-gray-700 dark:text-gray-300'}>
                        {addon.name}
                      </span>
                      {addon.already_migrated && (
                        <span className="rounded bg-blue-100 px-1.5 py-0.5 text-xs text-blue-600 dark:bg-blue-900/30 dark:text-blue-400">
                          already migrated
                        </span>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
            Cancel
          </Button>
          <Button onClick={handleStart} disabled={!canStart}>
            {loading && <Loader2 className="h-4 w-4 animate-spin" />}
            {scope === 'cluster'
              ? `Migrate ${clusterAddons.filter(a => !a.already_migrated).length} Addons`
              : scope === 'multiple'
                ? `Migrate ${selectedAddons.length} Addon${selectedAddons.length !== 1 ? 's' : ''}`
                : 'Start Migration'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
