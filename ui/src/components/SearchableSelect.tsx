import { useState, useRef, useEffect } from 'react'
import { ChevronDown, X } from 'lucide-react'
import { cn } from '@/lib/utils'

interface SearchableSelectProps {
  options: string[]
  value: string
  onChange: (value: string) => void
  placeholder?: string
  disabled?: boolean
  loading?: boolean
}

export function SearchableSelect({
  options,
  value,
  onChange,
  placeholder = 'Select...',
  disabled = false,
  loading = false,
}: SearchableSelectProps) {
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const ref = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const filtered = options.filter((o) =>
    o.toLowerCase().includes(search.toLowerCase())
  )

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  return (
    <div ref={ref} className="relative">
      <div
        className={cn(
          'flex h-9 w-full items-center rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs',
          'focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/50',
          'dark:bg-input/30',
          disabled && 'cursor-not-allowed opacity-50'
        )}
        onClick={() => {
          if (!disabled) {
            setOpen(true)
            setTimeout(() => inputRef.current?.focus(), 0)
          }
        }}
      >
        {open ? (
          <input
            ref={inputRef}
            className="flex-1 bg-transparent outline-none placeholder:text-muted-foreground"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={value || placeholder}
          />
        ) : (
          <span className={cn('flex-1 truncate', !value && 'text-muted-foreground')}>
            {value || placeholder}
          </span>
        )}
        {value && !disabled ? (
          <X
            className="h-4 w-4 shrink-0 cursor-pointer text-gray-400 hover:text-gray-600"
            onClick={(e) => {
              e.stopPropagation()
              onChange('')
              setSearch('')
            }}
          />
        ) : (
          <ChevronDown className="h-4 w-4 shrink-0 text-gray-400" />
        )}
      </div>

      {open && (
        <div className="absolute z-50 mt-1 max-h-60 w-full overflow-auto rounded-md border border-gray-200 bg-white shadow-lg dark:border-gray-700 dark:bg-gray-800">
          {loading ? (
            <div className="px-3 py-2 text-sm text-gray-500">Loading...</div>
          ) : filtered.length === 0 ? (
            <div className="px-3 py-2 text-sm text-gray-500">
              {search ? 'No matches found' : 'No options available'}
            </div>
          ) : (
            filtered.map((option) => (
              <div
                key={option}
                className={cn(
                  'cursor-pointer px-3 py-2 text-sm hover:bg-gray-100 dark:hover:bg-gray-700',
                  option === value && 'bg-gray-100 font-medium dark:bg-gray-700'
                )}
                onClick={() => {
                  onChange(option)
                  setSearch('')
                  setOpen(false)
                }}
              >
                {option}
              </div>
            ))
          )}
        </div>
      )}
    </div>
  )
}
