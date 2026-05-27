import { useEffect, useState, useCallback } from 'react'
import type { MemoryTreeNode } from '../../api/client'
import { getMemoryTree } from '../../api/client'
import { SynthesisModal } from './SynthesisModal'

interface FileTreeProps {
  onFileSelect: (path: string) => void
  selectedPath?: string
}

function fileIcon(name: string): string {
  const ext = name.split('.').pop()?.toLowerCase() ?? ''
  switch (ext) {
    case 'md':
    case 'markdown':
      return '📝'
    case 'json':
      return '📋'
    case 'yaml':
    case 'yml':
      return '⚙️'
    case 'txt':
      return '📄'
    case 'png':
    case 'jpg':
    case 'jpeg':
    case 'gif':
    case 'webp':
      return '🖼️'
    case 'py':
    case 'js':
    case 'ts':
      return '💻'
    case 'pdf':
      return '📕'
    default:
      return '📄'
  }
}

function collectFilePaths(node: MemoryTreeNode): string[] {
  if (node.type === 'file') return [node.path]
  const results: string[] = []
  if (node.children) {
    for (const child of node.children) {
      results.push(...collectFilePaths(child))
    }
  }
  return results
}

function isFolder(node: MemoryTreeNode): boolean {
  return node.type === 'dir' || node.type === 'folder'
}

function TreeNode({
  node,
  onFileSelect,
  selectedPath,
  depth,
  selectedPaths,
  onToggleSelect,
}: {
  node: MemoryTreeNode
  onFileSelect: (path: string) => void
  selectedPath?: string
  depth: number
  selectedPaths: Set<string>
  onToggleSelect: (path: string, isFolder: boolean) => void
}) {
  const [expanded, setExpanded] = useState(false)
  const [children, setChildren] = useState<MemoryTreeNode[] | null>(node.children ?? null)
  const [loading, setLoading] = useState(false)
  const isSelected = selectedPaths.has(node.path)

  const loadChildren = useCallback(async () => {
    if (children !== null) return // already loaded
    setLoading(true)
    try {
      const loaded = await getMemoryTree(node.path, 0)
      const filtered = loaded.filter((n) => n.name !== '.obsidian')
      setChildren(filtered)
    } catch {
      setChildren([])
    } finally {
      setLoading(false)
    }
  }, [node.path, children])

  function handleToggle() {
    const next = !expanded
    setExpanded(next)
    if (next && children === null) {
      loadChildren()
    }
  }

  if (isFolder(node)) {
    return (
      <div>
        <div
          className="w-full flex items-center gap-1.5 px-2 py-1 text-sm text-gray-300 hover:bg-gray-800 hover:text-white rounded transition-colors"
          style={{ paddingLeft: `${depth * 16 + 8}px` }}
        >
          <input
            type="checkbox"
            id={`cb-${node.path}`}
            checked={isSelected}
            onChange={(e) => {
              e.stopPropagation()
              onToggleSelect(node.path, true)
            }}
            aria-label={`Select folder ${node.name}`}
            className="h-3.5 w-3.5 rounded border-gray-600 bg-gray-800 text-blue-500 focus:ring-blue-500 focus:ring-offset-0 cursor-pointer accent-blue-500"
          />
          <button
            onClick={handleToggle}
            className="flex items-center gap-1.5 flex-1 min-w-0"
            aria-expanded={expanded}
          >
            <span className="text-xs">{expanded ? '📂' : '📁'}</span>
            <span className="truncate">{node.name}</span>
            {loading && <span className="text-xs text-gray-500 ml-1">…</span>}
            <span className="text-gray-600 text-xs ml-auto">
              {expanded ? '▾' : '▸'}
            </span>
          </button>
        </div>
        {expanded && children && (
          <div>
            {children.map((child) => (
              <TreeNode
                key={child.path}
                node={child}
                onFileSelect={onFileSelect}
                selectedPath={selectedPath}
                depth={depth + 1}
                selectedPaths={selectedPaths}
                onToggleSelect={onToggleSelect}
              />
            ))}
          </div>
        )}
      </div>
    )
  }

  return (
    <div
      className={`w-full flex items-center gap-1.5 px-2 py-1 text-sm rounded transition-colors ${
        selectedPath === node.path
          ? 'bg-blue-600/20 text-blue-300'
          : 'text-gray-400 hover:bg-gray-800 hover:text-white'
      }`}
      style={{ paddingLeft: `${depth * 16 + 8}px` }}
    >
      <input
        type="checkbox"
        id={`cb-${node.path}`}
        checked={isSelected}
        onChange={() => onToggleSelect(node.path, false)}
        aria-label={`Select file ${node.name}`}
        className="h-3.5 w-3.5 rounded border-gray-600 bg-gray-800 text-blue-500 focus:ring-blue-500 focus:ring-offset-0 cursor-pointer accent-blue-500"
      />
      <button
        onClick={() => onFileSelect(node.path)}
        className="flex items-center gap-1.5 flex-1 min-w-0"
      >
        <span className="text-xs">{fileIcon(node.name)}</span>
        <span className="truncate">{node.name}</span>
      </button>
    </div>
  )
}

export function FileTree({ onFileSelect, selectedPath }: FileTreeProps) {
  const [tree, setTree] = useState<MemoryTreeNode[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [selectedPaths, setSelectedPaths] = useState<Set<string>>(new Set())
  const [synthesisOpen, setSynthesisOpen] = useState(false)

  useEffect(() => {
    setLoading(true)
    setError(null)
    // Load root with depth=0 (lazy loading — only top-level items, no children)
    getMemoryTree(undefined, 0)
      .then((nodes) => setTree(nodes.filter((n) => n.name !== '.obsidian')))
      .catch((err) => setError(err.message))
      .finally(() => setLoading(false))
  }, [])

  function handleToggleSelect(path: string, isFolder: boolean) {
    setSelectedPaths((prev) => {
      const next = new Set(prev)
      if (next.has(path)) {
        next.delete(path)
      } else if (isFolder) {
        // Find the node and add all its file paths
        const findNode = (nodes: MemoryTreeNode[]): MemoryTreeNode | null => {
          for (const n of nodes) {
            if (n.path === path) return n
            if (n.children) {
              const found = findNode(n.children)
              if (found) return found
            }
          }
          return null
        }
        const node = findNode(tree)
        if (node) {
          for (const fp of collectFilePaths(node)) {
            next.add(fp)
          }
        }
      } else {
        next.add(path)
      }
      return next
    })
  }

  function clearSelection() {
    setSelectedPaths(new Set())
  }

  if (loading) {
    return (
      <div className="p-4 text-gray-500 text-sm">Loading file tree…</div>
    )
  }

  if (error) {
    return (
      <div className="p-4 text-red-400 text-sm">Error: {error}</div>
    )
  }

  if (tree.length === 0) {
    return (
      <div className="p-4 text-gray-500 text-sm">No files found.</div>
    )
  }

  const filePaths = Array.from(selectedPaths)

  return (
    <div className="relative flex flex-col h-full">
      <div className="flex-1 overflow-y-auto py-2">
        {tree.map((node) => (
          <TreeNode
            key={node.path}
            node={node}
            onFileSelect={onFileSelect}
            selectedPath={selectedPath}
            depth={0}
            selectedPaths={selectedPaths}
            onToggleSelect={handleToggleSelect}
          />
        ))}
      </div>

      {/* Floating action bar */}
      {filePaths.length >= 2 && (
        <div className="flex-shrink-0 border-t border-gray-700 bg-gray-900 px-3 py-2.5 flex items-center gap-2">
          <span className="text-xs text-gray-400">
            {filePaths.length} files selected
          </span>
          <button
            onClick={() => setSynthesisOpen(true)}
            className="ml-auto px-3 py-1 text-xs font-medium rounded bg-blue-600 text-white hover:bg-blue-500 transition-colors"
          >
            ✨ Synthesize
          </button>
          <button
            onClick={clearSelection}
            className="px-2 py-1 text-xs text-gray-400 hover:text-white transition-colors"
          >
            Clear
          </button>
        </div>
      )}

      {/* Synthesis modal */}
      {synthesisOpen && (
        <SynthesisModal
          filePaths={filePaths}
          onClose={() => {
            setSynthesisOpen(false)
            clearSelection()
          }}
        />
      )}
    </div>
  )
}
