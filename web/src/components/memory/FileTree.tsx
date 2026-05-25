import { useEffect, useState } from 'react'
import type { MemoryTreeNode } from '../../api/client'
import { getMemoryTree } from '../../api/client'

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

function TreeNode({
  node,
  onFileSelect,
  selectedPath,
  depth,
}: {
  node: MemoryTreeNode
  onFileSelect: (path: string) => void
  selectedPath?: string
  depth: number
}) {
  const [expanded, setExpanded] = useState(depth < 1)

  if (node.type === 'folder') {
    return (
      <div>
        <button
          onClick={() => setExpanded(!expanded)}
          className="w-full flex items-center gap-1.5 px-2 py-1 text-sm text-gray-300 hover:bg-gray-800 hover:text-white rounded transition-colors"
          style={{ paddingLeft: `${depth * 16 + 8}px` }}
        >
          <span className="text-xs">{expanded ? '📂' : '📁'}</span>
          <span className="truncate">{node.name}</span>
          <span className="text-gray-600 text-xs ml-auto">
            {expanded ? '▾' : '▸'}
          </span>
        </button>
        {expanded && node.children && (
          <div>
            {node.children
              .filter((child) => child.name !== '.obsidian')
              .map((child) => (
                <TreeNode
                  key={child.path}
                  node={child}
                  onFileSelect={onFileSelect}
                  selectedPath={selectedPath}
                  depth={depth + 1}
                />
              ))}
          </div>
        )}
      </div>
    )
  }

  return (
    <button
      onClick={() => onFileSelect(node.path)}
      className={`w-full flex items-center gap-1.5 px-2 py-1 text-sm rounded transition-colors ${
        selectedPath === node.path
          ? 'bg-blue-600/20 text-blue-300'
          : 'text-gray-400 hover:bg-gray-800 hover:text-white'
      }`}
      style={{ paddingLeft: `${depth * 16 + 8}px` }}
    >
      <span className="text-xs">{fileIcon(node.name)}</span>
      <span className="truncate">{node.name}</span>
    </button>
  )
}

export function FileTree({ onFileSelect, selectedPath }: FileTreeProps) {
  const [tree, setTree] = useState<MemoryTreeNode[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setLoading(true)
    setError(null)
    getMemoryTree()
      .then((nodes) => setTree(nodes.filter((n) => n.name !== '.obsidian')))
      .catch((err) => setError(err.message))
      .finally(() => setLoading(false))
  }, [])

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

  return (
    <div className="py-2">
      {tree.map((node) => (
        <TreeNode
          key={node.path}
          node={node}
          onFileSelect={onFileSelect}
          selectedPath={selectedPath}
          depth={0}
        />
      ))}
    </div>
  )
}
