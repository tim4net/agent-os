import { useCallback, useEffect, useRef, useState } from 'react'
import type { MemorySearchResult } from '../../api/client'
import { searchMemory } from '../../api/client'

interface SearchBarProps {
  onFileSelect: (path: string) => void
}

export function SearchBar({ onFileSelect }: SearchBarProps) {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<MemorySearchResult[]>([])
  const [loading, setLoading] = useState(false)
  const [showResults, setShowResults] = useState(false)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const containerRef = useRef<HTMLDivElement>(null)

  const doSearch = useCallback(async (q: string) => {
    if (q.trim().length < 2) {
      setResults([])
      setShowResults(false)
      return
    }
    setLoading(true)
    try {
      const res = await searchMemory(q)
      setResults(res)
      setShowResults(true)
    } catch {
      setResults([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => doSearch(query), 300)
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [query, doSearch])

  // Close dropdown when clicking outside
  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setShowResults(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  function handleSelect(result: MemorySearchResult) {
    onFileSelect(result.path)
    setShowResults(false)
    setQuery('')
  }

  return (
    <div ref={containerRef} className="relative">
      <div className="relative">
        <span className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-500 text-sm">🔍</span>
        <input
          type="text"
          placeholder="Search memory…"
          aria-label="Search memory"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onFocus={() => results.length > 0 && setShowResults(true)}
          className="w-full bg-gray-800 text-gray-200 text-sm pl-9 pr-3 py-2 rounded-lg border border-gray-700 focus:border-blue-500 focus:outline-none placeholder-gray-500"
        />
        {loading && (
          <span className="absolute right-3 top-1/2 -translate-y-1/2 text-gray-500 text-xs">
            …
          </span>
        )}
      </div>

      {showResults && results.length > 0 && (
        <div className="absolute z-50 w-full mt-1 bg-gray-800 border border-gray-700 rounded-lg shadow-xl max-h-64 overflow-y-auto">
          {results.map((result) => (
            <button
              key={result.path}
              onClick={() => handleSelect(result)}
              className="w-full text-left px-3 py-2 hover:bg-gray-700 transition-colors border-b border-gray-700/50 last:border-0"
            >
              <p className="text-sm text-white truncate">{result.title}</p>
              <p className="text-xs text-gray-400 truncate">{result.path}</p>
              {result.snippet && (
                <p className="text-xs text-gray-500 truncate mt-0.5">{result.snippet}</p>
              )}
            </button>
          ))}
        </div>
      )}

      {showResults && results.length === 0 && query.trim().length >= 2 && !loading && (
        <div className="absolute z-50 w-full mt-1 bg-gray-800 border border-gray-700 rounded-lg shadow-xl p-3">
          <p className="text-sm text-gray-400">No results found.</p>
        </div>
      )}
    </div>
  )
}
