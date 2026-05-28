import { useEffect, useState, useCallback } from 'react'

/* ─── Types ─── */
interface Agent {
  id: string
  name: string
  display_name: string
  status: string
  harness: string
  role?: string
}

interface Delegation {
  id: string
  child_agent_name: string
  task_goal: string
  status: string
  result_summary: string | null
  created_at: string
  completed_at: string | null
}

interface Goal {
  id: string
  title: string
  status: string
  progress: number
  description: string | null
}

interface TaskSummary {
  backlog: number
  in_progress: number
  review: number
  done: number
}

interface HealthData {
  status: string
  uptime: string
  version: string
  components: Record<string, string>
}

/* ─── Helpers ─── */
function timeAgo(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  const mins = Math.floor(diff / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  return `${Math.floor(hrs / 24)}d ago`
}

const statusColor: Record<string, string> = {
  online: 'bg-emerald-500 shadow-[0_0_8px_rgba(34,197,94,0.5)]',
  offline: 'bg-gray-500',
  degraded: 'bg-amber-500 shadow-[0_0_8px_rgba(245,158,11,0.4)]',
  unknown: 'bg-gray-600',
}

const delegationColor: Record<string, string> = {
  pending: 'text-amber-400',
  running: 'text-blue-400',
  completed: 'text-emerald-400',
  failed: 'text-red-400',
  interrupted: 'text-gray-400',
}

const goalBadge: Record<string, string> = {
  active: 'bg-blue-500/20 text-blue-400',
  completed: 'bg-emerald-500/20 text-emerald-400',
  paused: 'bg-amber-500/20 text-amber-400',
  abandoned: 'bg-red-500/20 text-red-400',
}

/* ─── Component ─── */
export default function MissionControl({ agents }: { agents: Agent[] }) {
  const [health, setHealth] = useState<HealthData | null>(null)
  const [delegations, setDelegations] = useState<Delegation[]>([])
  const [goals, setGoals] = useState<Goal[]>([])
  const [taskSummary, setTaskSummary] = useState<TaskSummary>({ backlog: 0, in_progress: 0, review: 0, done: 0 })
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    try {
      const [hRes, dRes, gRes, tRes] = await Promise.all([
        fetch('/api/health').then(r => r.json()),
        fetch('/api/delegations').then(r => r.json()),
        fetch('/api/goals').then(r => r.json()),
        fetch('/api/tasks').then(r => r.json()),
      ])
      setHealth(hRes)
      setDelegations((dRes.delegations || []).slice(0, 8))
      setGoals(Array.isArray(gRes) ? gRes.slice(0, 6) : [])
      const tasks = Array.isArray(tRes) ? tRes : []
      const ts: Record<string, number> = { backlog: 0, in_progress: 0, review: 0, done: 0 }
      for (const t of tasks) {
        const s = t.status || 'backlog'
        ts[s] = (ts[s] || 0) + 1
      }
      setTaskSummary(ts as unknown as TaskSummary)
    } catch (e) {
      console.error('MissionControl load failed:', e)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
    const iv = setInterval(load, 30000)
    return () => clearInterval(iv)
  }, [load])

  const onlineCount = agents.filter(a => a.status === 'online').length
  const activeDelegations = delegations.filter(d => d.status === 'running' || d.status === 'pending')
  const totalTasks = taskSummary.backlog + taskSummary.in_progress + taskSummary.review + taskSummary.done

  if (loading) {
    return (
      <div className="p-6 space-y-6">
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
          {[...Array(4)].map((_, i) => (
            <div key={i} className="glass-card p-4 animate-pulse">
              <div className="h-3 bg-[var(--bg-elevated)] rounded w-1/2 mb-2" />
              <div className="h-6 bg-[var(--bg-elevated)] rounded w-3/4" />
            </div>
          ))}
        </div>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between mb-2">
        <h2 className="text-2xl font-bold text-[var(--color-text-primary)]">Mission Control</h2>
        {health && (
          <span className="text-xs text-[var(--color-text-muted)] font-mono">
            v{health.version} · up {health.uptime}
          </span>
        )}
      </div>

      {/* ── Stats Bar ── */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard label="Agents Online" value={`${onlineCount}/${agents.length}`} icon="🤖" accent={onlineCount === agents.length ? 'emerald' : 'amber'} />
        <StatCard label="Active Tasks" value={`${taskSummary.in_progress + taskSummary.review}`} subtext={`${totalTasks} total`} icon="📋" accent="blue" />
        <StatCard label="Running Delegations" value={`${activeDelegations.length}`} subtext={`${delegations.length} total`} icon="⚡" accent={activeDelegations.length > 0 ? 'blue' : 'gray'} />
        <StatCard label="Active Goals" value={`${goals.filter(g => g.status === 'active').length}`} subtext={`${goals.length} total`} icon="🎯" accent="purple" />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        {/* ── Agent Fleet ── */}
        <div className="lg:col-span-1">
          <h3 className="text-sm font-semibold mb-3 text-[var(--color-text-secondary)] uppercase tracking-wider">Agent Fleet</h3>
          <div className="space-y-2">
            {agents.map(agent => (
              <div key={agent.id} className="glass-card p-3 flex items-center gap-3">
                <span className={`w-2.5 h-2.5 rounded-full flex-shrink-0 ${statusColor[agent.status] || statusColor.unknown}`} />
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-semibold text-[var(--color-text-primary)] truncate">{agent.display_name || agent.name}</p>
                  <p className="text-xs text-[var(--color-text-muted)] truncate">{agent.harness}{agent.role ? ` · ${agent.role.slice(0, 40)}` : ''}</p>
                </div>
                <span className={`text-xs font-medium ${agent.status === 'online' ? 'text-emerald-400' : 'text-gray-500'}`}>
                  {agent.status}
                </span>
              </div>
            ))}
          </div>
        </div>

        {/* ── Task Pipeline ── */}
        <div className="lg:col-span-1">
          <h3 className="text-sm font-semibold mb-3 text-[var(--color-text-secondary)] uppercase tracking-wider">Task Pipeline</h3>
          <div className="glass-card p-4 space-y-4">
            {/* Progress bar */}
            <div className="w-full h-3 bg-[var(--bg-elevated)] rounded-full overflow-hidden flex">
              {totalTasks > 0 && (
                <>
                  <div style={{ width: `${(taskSummary.done / totalTasks) * 100}%` }} className="bg-emerald-500 transition-all duration-500" />
                  <div style={{ width: `${(taskSummary.review / totalTasks) * 100}%` }} className="bg-blue-500 transition-all duration-500" />
                  <div style={{ width: `${(taskSummary.in_progress / totalTasks) * 100}%` }} className="bg-amber-500 transition-all duration-500" />
                  <div style={{ width: `${(taskSummary.backlog / totalTasks) * 100}%` }} className="bg-gray-600 transition-all duration-500" />
                </>
              )}
            </div>
            <div className="grid grid-cols-2 gap-3">
              <KanbanStat label="Backlog" count={taskSummary.backlog} color="text-gray-400" />
              <KanbanStat label="In Progress" count={taskSummary.in_progress} color="text-amber-400" />
              <KanbanStat label="Review" count={taskSummary.review} color="text-blue-400" />
              <KanbanStat label="Done" count={taskSummary.done} color="text-emerald-400" />
            </div>

            {/* Goals */}
            <div className="pt-2 border-t border-[var(--border-subtle)]">
              <p className="text-xs text-[var(--color-text-muted)] mb-2 uppercase tracking-wider">Goals</p>
              {goals.length === 0 ? (
                <p className="text-xs text-[var(--color-text-muted)]">No goals set</p>
              ) : (
                <div className="space-y-2">
                  {goals.map(g => (
                    <div key={g.id} className="flex items-center gap-2">
                      <span className={`text-xs px-2 py-0.5 rounded-full ${goalBadge[g.status] || 'bg-gray-500/20 text-gray-400'}`}>
                        {g.status}
                      </span>
                      <span className="text-xs text-[var(--color-text-primary)] truncate">{g.title}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        </div>

        {/* ── Live Delegations ── */}
        <div className="lg:col-span-1">
          <h3 className="text-sm font-semibold mb-3 text-[var(--color-text-secondary)] uppercase tracking-wider">Delegations</h3>
          <div className="space-y-2">
            {delegations.length === 0 ? (
              <div className="glass-card p-4">
                <p className="text-xs text-[var(--color-text-muted)]">No delegations yet</p>
              </div>
            ) : (
              delegations.map(d => (
                <div key={d.id} className="glass-card p-3">
                  <div className="flex items-center justify-between mb-1">
                    <span className="text-xs font-mono text-[var(--color-text-muted)]">{d.child_agent_name}</span>
                    <span className={`text-xs font-semibold ${delegationColor[d.status] || 'text-gray-400'}`}>
                      {d.status.toUpperCase()}
                    </span>
                  </div>
                  <p className="text-xs text-[var(--color-text-primary)] line-clamp-2">{d.task_goal}</p>
                  <div className="flex items-center justify-between mt-1">
                    <span className="text-[10px] text-[var(--color-text-muted)]">{timeAgo(d.created_at)}</span>
                    {d.result_summary && (
                      <span className="text-[10px] text-[var(--color-text-muted)] truncate max-w-[60%]">{d.result_summary.slice(0, 50)}</span>
                    )}
                  </div>
                </div>
              ))
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

/* ── Sub-components ── */
function StatCard({ label, value, subtext, icon, accent }: {
  label: string, value: string, subtext?: string, icon: string, accent: string
}) {
  const colorMap: Record<string, string> = {
    emerald: 'from-emerald-500/10 to-transparent border-emerald-500/20',
    blue: 'from-blue-500/10 to-transparent border-blue-500/20',
    amber: 'from-amber-500/10 to-transparent border-amber-500/20',
    purple: 'from-purple-500/10 to-transparent border-purple-500/20',
    gray: 'from-gray-500/10 to-transparent border-gray-500/20',
  }
  return (
    <div className={`glass-card p-4 bg-gradient-to-br ${colorMap[accent] || colorMap.gray}`}>
      <div className="flex items-center gap-2 mb-1">
        <span className="text-base">{icon}</span>
        <span className="text-xs text-[var(--color-text-muted)]">{label}</span>
      </div>
      <p className="text-2xl font-bold text-[var(--color-text-primary)]">{value}</p>
      {subtext && <p className="text-[10px] text-[var(--color-text-muted)]">{subtext}</p>}
    </div>
  )
}

function KanbanStat({ label, count, color }: { label: string, count: number, color: string }) {
  return (
    <div className="flex items-center justify-between">
      <span className={`text-xs ${color}`}>{label}</span>
      <span className="text-sm font-bold text-[var(--color-text-primary)]">{count}</span>
    </div>
  )
}
