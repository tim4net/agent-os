import { useEffect, useState, useMemo } from 'react'
import { getMemoryFile, saveMemoryFile } from '../../api/client'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { Components } from 'react-markdown'
import { Icon } from '../Icon'

interface NoteViewerProps {
  filePath: string | null
}

export function NoteViewer({ filePath }: NoteViewerProps) {
  const [content, setContent] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)
  const [editContent, setEditContent] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!filePath) {
      setContent(null)
      setEditing(false)
      return
    }
    setLoading(true)
    setError(null)
    setEditing(false)
    getMemoryFile(filePath)
      .then((file) => setContent(file.content))
      .catch((err) => setError(err.message))
      .finally(() => setLoading(false))
  }, [filePath])

  function startEdit() {
    if (content === null) return
    setEditContent(content)
    setEditing(true)
  }

  async function handleSave() {
    if (!filePath) return
    setSaving(true)
    setError(null)
    try {
      const saved = await saveMemoryFile(filePath, editContent)
      setContent(saved.content)
      setEditing(false)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  function cancelEdit() {
    setEditing(false)
    setEditContent('')
  }

  if (!filePath) {
    return (
      <div className="flex items-center justify-center h-full text-gray-500">
        Select a file from the tree to view its contents.
      </div>
    )
  }

  const fileName = filePath.split('/').pop() ?? filePath

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-gray-800 flex-shrink-0">
        <div className="flex items-center gap-2 min-w-0">
          <Icon name="edit_note" size={20} />
          <h3 className="text-sm font-semibold text-white truncate">{fileName}</h3>
          <span className="text-xs text-gray-500 truncate">{filePath}</span>
        </div>
        {!editing && content !== null && (
          <button
            onClick={startEdit}
            className="px-3 py-1 text-xs bg-gray-800 text-gray-300 hover:bg-gray-700 hover:text-white rounded transition-colors flex-shrink-0"
          >
            Edit
          </button>
        )}
      </div>

      {/* Error */}
      {error && (
        <div className="px-4 py-2 bg-red-900/20 text-red-400 text-sm border-b border-red-900/30">
          {error}
        </div>
      )}

      {/* Body */}
      <div className="flex-1 overflow-auto">
        {loading ? (
          <div className="p-4 text-gray-500 text-sm">Loading…</div>
        ) : editing ? (
          <div className="flex flex-col h-full">
            <textarea
              value={editContent}
              onChange={(e) => setEditContent(e.target.value)}
              className="flex-1 w-full bg-gray-900 text-gray-200 font-mono text-sm p-4 resize-none border-none outline-none"
              spellCheck={false}
            />
            <div className="flex items-center gap-2 px-4 py-2 border-t border-gray-800 bg-gray-900 flex-shrink-0">
              <button
                onClick={handleSave}
                disabled={saving}
                className="px-4 py-1.5 text-xs bg-blue-600 text-white hover:bg-blue-500 rounded transition-colors disabled:opacity-50"
              >
                {saving ? 'Saving…' : 'Save'}
              </button>
              <button
                onClick={cancelEdit}
                disabled={saving}
                className="px-4 py-1.5 text-xs bg-gray-700 text-gray-300 hover:bg-gray-600 rounded transition-colors disabled:opacity-50"
              >
                Cancel
              </button>
            </div>
          </div>
        ) : content !== null ? (
          <div className="p-4 text-sm text-gray-200 leading-relaxed overflow-x-auto">
            <MarkdownWithWikilinks content={content} />
          </div>
        ) : null}
      </div>
    </div>
  )
}

/** Pre-process Obsidian wikilinks like [[path|Display Text]] into styled HTML spans */
function preprocessWikilinks(text: string): string {
  return text.replace(/\[\[([^\]]+)\]\]/g, (_match, inner: string) => {
    const parts = inner.split('|')
    const display = parts.length > 1 ? parts[1]!.trim() : parts[0]!.trim()
    // Use a unique marker that markdown won't mangle — we swap it back in a custom component
    return `<wikilink>${display}</wikilink>`
  })
}

/** Regex to detect our wikilink pseudo-HTML */
const WIKILINK_RE = /<wikilink>([^<]*)<\/wikilink>/

/** Split text around the first wikilink, return null if none found */
function splitWikilink(text: string): null | { before: string; link: string; after: string } {
  const m = text.match(WIKILINK_RE)
  if (!m || m.index === undefined) return null
  return { before: text.slice(0, m.index), link: m[1]!, after: text.slice(m.index + m[0].length) }
}

/** Render markdown text that may contain wikilink pseudo-HTML */
function WikilinkText({ text }: { text: string }) {
  const parts: React.ReactNode[] = []
  let remaining = text
  let key = 0
  while (remaining) {
    const split = splitWikilink(remaining)
    if (!split) {
      parts.push(remaining)
      break
    }
    if (split.before) parts.push(split.before)
    parts.push(
      <span
        key={key++}
        className="text-blue-400 underline decoration-blue-400/40 underline-offset-2 cursor-default bg-blue-400/10 px-0.5 rounded-sm"
        title="Obsidian wikilink (not navigable)"
      >
        {split.link}
      </span>
    )
    remaining = split.after
  }
  return <>{parts}</>
}

/** Dark-theme markdown components override */
const mdComponents: Components = {
  h1: ({ children }) => <h1 className="text-2xl font-bold text-white mt-6 mb-3 first:mt-0">{children}</h1>,
  h2: ({ children }) => <h2 className="text-xl font-bold text-white mt-5 mb-2">{children}</h2>,
  h3: ({ children }) => <h3 className="text-lg font-semibold text-white mt-4 mb-2">{children}</h3>,
  h4: ({ children }) => <h4 className="text-base font-semibold text-white mt-3 mb-1">{children}</h4>,
  p: ({ children }) => <p className="mb-3 last:mb-0 leading-relaxed">{children}</p>,
  ul: ({ children }) => <ul className="list-disc list-outside ml-5 mb-3 space-y-1">{children}</ul>,
  ol: ({ children }) => <ol className="list-decimal list-outside ml-5 mb-3 space-y-1">{children}</ol>,
  li: ({ children }) => <li className="leading-relaxed">{children}</li>,
  blockquote: ({ children }) => <blockquote className="border-l-3 border-gray-600 pl-4 italic text-gray-400 my-3">{children}</blockquote>,
  hr: () => <hr className="border-gray-700 my-4" />,
  a: ({ href, children }) => (
    <a href={href} className="text-blue-400 underline underline-offset-2 hover:text-blue-300" target="_blank" rel="noopener noreferrer">
      {children}
    </a>
  ),
  code: ({ className, children }) => {
    const isInline = !className
    if (isInline) {
      return <code className="bg-gray-800 text-pink-300 px-1.5 py-0.5 rounded text-[0.85em] font-mono">{children}</code>
    }
    return (
      <code className={`${className ?? ''} block text-[0.85em]`}>{children}</code>
    )
  },
  pre: ({ children }) => (
    <pre className="bg-gray-900 border border-gray-800 rounded-lg p-3 my-3 overflow-x-auto text-[0.85em] leading-relaxed">
      {children}
    </pre>
  ),
  table: ({ children }) => (
    <div className="overflow-x-auto my-3">
      <table className="min-w-full border-collapse border border-gray-700">{children}</table>
    </div>
  ),
  thead: ({ children }) => <thead className="bg-gray-800/50">{children}</thead>,
  th: ({ children }) => <th className="border border-gray-700 px-3 py-1.5 text-left text-gray-300 font-semibold">{children}</th>,
  td: ({ children }) => <td className="border border-gray-700 px-3 py-1.5 text-gray-300">{children}</td>,
  tr: ({ children }) => <tr className="even:bg-gray-800/30">{children}</tr>,
  del: ({ children }) => <del className="line-through text-gray-500">{children}</del>,
  strong: ({ children }) => <strong className="font-semibold text-white">{children}</strong>,
  em: ({ children }) => <em className="italic text-gray-300">{children}</em>,
}

function MarkdownWithWikilinks({ content }: { content: string }) {
  const processed = useMemo(() => preprocessWikilinks(content), [content])

  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        ...mdComponents,
        // Intercept raw text nodes to render wikilinks
        text: ({ children }) => {
          const text = String(children)
          if (WIKILINK_RE.test(text)) {
            return <WikilinkText text={text} />
          }
          return <>{text}</>
        },
      }}
    >
      {processed}
    </ReactMarkdown>
  )
}