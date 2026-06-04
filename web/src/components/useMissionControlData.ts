import { useEffect, useState, useCallback, useRef } from 'react'
import { 
  listIncidents, 
  getSpend, 
  getFleet, 
  getRecurringFindings,
  getControlState
} from '../api/client'
import type { 
  Incident, 
  SpendRow, 
  SessionStatus, 
  RecurringFindingsRow,
  ControlState
} from '../api/client'

type TenantFilter = 'all' | 'personal' | 'dayjob'

export function useIncidents(tenantFilter: TenantFilter) {
  const [incidents, setIncidents] = useState<Incident[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(false)
  const controllersRef = useRef<Set<AbortController>>(new Set())

  const refresh = useCallback(() => {
    const abortController = new AbortController()
    const signal = abortController.signal
    controllersRef.current.add(abortController)
    
    let promise;
    if (tenantFilter === 'all') {
      promise = Promise.allSettled([
        listIncidents('personal', { limit: 50 }),
        listIncidents('dayjob', { limit: 50 })
      ]).then((results) => {
        const successes = results.flatMap((result) => result.status === 'fulfilled' ? [result.value] : []);
        if (successes.length === 0) {
          const reason = results.find((result) => result.status === 'rejected')?.reason;
          throw new Error(reason instanceof Error ? reason.message : 'Failed to fetch incidents');
        }

        results.forEach((result) => {
          if (result.status === 'rejected') console.error(result.reason);
        });

        const merged = successes.flatMap((res) => res.incidents ?? []);
        merged.sort((a, b) => new Date(b.received_at).getTime() - new Date(a.received_at).getTime());
        return {
          incidents: merged,
          total: successes.reduce((sum, res) => sum + (res.total ?? 0), 0)
        };
      });
    } else {
      promise = listIncidents(tenantFilter, { limit: 50 });
    }

    promise
      .then((res) => {
        if (!signal.aborted && mountedRef.current) {
          setIncidents(res.incidents ?? [])
          setTotal(res.total ?? 0)
          setError(null)
        }
      })
      .catch((err) => {
        if (!signal.aborted && mountedRef.current) {
          setError(err instanceof Error ? err.message : 'Failed to fetch incidents')
        }
      })
      .finally(() => {
        controllersRef.current.delete(abortController)
        if (!signal.aborted && mountedRef.current) {
          setLoading(false)
        }
      })

    return () => {
      abortController.abort()
      controllersRef.current.delete(abortController)
    }
  }, [tenantFilter])

  useEffect(() => {
    mountedRef.current = true
    const controllers = controllersRef.current
    // eslint-disable-next-line react-hooks/set-state-in-effect -- starts async incident fetch/polling on mount or tenant change; loading tracks external request lifecycle, not render-derived state.
    setLoading(true)
    const cleanup = refresh()
    const interval = setInterval(refresh, 20000)
    return () => {
      mountedRef.current = false
      cleanup?.()
      controllers.forEach((controller) => controller.abort())
      controllers.clear()
      clearInterval(interval)
    }
  }, [refresh])

  return { incidents, total, loading, error, refresh }
}

export function useFleet(tenantFilter: TenantFilter) {
  const [sessions, setSessions] = useState<SessionStatus[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(false)
  const controllersRef = useRef<Set<AbortController>>(new Set())

  const refresh = useCallback(() => {
    const abortController = new AbortController()
    const signal = abortController.signal
    controllersRef.current.add(abortController)

    let promise;
    if (tenantFilter === 'all') {
      promise = Promise.allSettled([
        getFleet('personal'),
        getFleet('dayjob')
      ]).then((results) => {
        const successes = results.flatMap((result) => result.status === 'fulfilled' ? [result.value] : []);
        if (successes.length === 0) {
          const reason = results.find((result) => result.status === 'rejected')?.reason;
          throw new Error(reason instanceof Error ? reason.message : 'Failed to fetch fleet');
        }

        results.forEach((result) => {
          if (result.status === 'rejected') console.error(result.reason);
        });

        const merged = successes.flatMap((res) => res.sessions ?? []);
        merged.sort((a, b) => new Date(b.last_event_at).getTime() - new Date(a.last_event_at).getTime());
        return {
          sessions: merged,
          total: successes.reduce((sum, res) => sum + (res.total ?? 0), 0)
        }
      });
    } else {
      promise = getFleet(tenantFilter);
    }

    promise
      .then((res) => {
        if (!signal.aborted && mountedRef.current) {
          setSessions(res.sessions ?? [])
          setError(null)
        }
      })
      .catch((err) => {
        if (!signal.aborted && mountedRef.current) {
          setError(err instanceof Error ? err.message : 'Failed to fetch fleet')
        }
      })
      .finally(() => {
        controllersRef.current.delete(abortController)
        if (!signal.aborted && mountedRef.current) {
          setLoading(false)
        }
      })

    return () => {
      abortController.abort()
      controllersRef.current.delete(abortController)
    }
  }, [tenantFilter])

  useEffect(() => {
    mountedRef.current = true
    const controllers = controllersRef.current
    // eslint-disable-next-line react-hooks/set-state-in-effect -- starts async fleet fetch/polling on mount or tenant change; loading tracks external request lifecycle, not render-derived state.
    setLoading(true)
    const cleanup = refresh()
    const interval = setInterval(refresh, 20000)
    return () => {
      mountedRef.current = false
      cleanup?.()
      controllers.forEach((controller) => controller.abort())
      controllers.clear()
      clearInterval(interval)
    }
  }, [refresh])

  return { sessions, loading, error, refresh }
}

export function useSpend(tenantFilter: TenantFilter) {
  const [rows, setRows] = useState<SpendRow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(false)
  const controllersRef = useRef<Set<AbortController>>(new Set())

  const refresh = useCallback(() => {
    const abortController = new AbortController()
    const signal = abortController.signal
    controllersRef.current.add(abortController)

    const tenant = tenantFilter === 'all' ? undefined : tenantFilter;
    getSpend({ group_by: 'agent', tenant })
      .then((res) => {
        if (!signal.aborted && mountedRef.current) {
          setRows(res.rows ?? [])
          setError(null)
        }
      })
      .catch((err) => {
        if (!signal.aborted && mountedRef.current) {
          setError(err instanceof Error ? err.message : 'Failed to fetch spend')
        }
      })
      .finally(() => {
        controllersRef.current.delete(abortController)
        if (!signal.aborted && mountedRef.current) {
          setLoading(false)
        }
      })

    return () => {
      abortController.abort()
      controllersRef.current.delete(abortController)
    }
  }, [tenantFilter])

  useEffect(() => {
    mountedRef.current = true
    const controllers = controllersRef.current
    // eslint-disable-next-line react-hooks/set-state-in-effect -- starts async spend fetch/polling on mount or tenant change; loading tracks external request lifecycle, not render-derived state.
    setLoading(true)
    const cleanup = refresh()
    const interval = setInterval(refresh, 20000)
    return () => {
      mountedRef.current = false
      cleanup?.()
      controllers.forEach((controller) => controller.abort())
      controllers.clear()
      clearInterval(interval)
    }
  }, [refresh])

  return { rows, loading, error, refresh }
}

export function useRecurringFindings(minCount = 2) {
  const [records, setRecords] = useState<RecurringFindingsRow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(false)
  const controllersRef = useRef<Set<AbortController>>(new Set())

  const refresh = useCallback(() => {
    const abortController = new AbortController()
    const signal = abortController.signal
    controllersRef.current.add(abortController)

    getRecurringFindings(minCount)
      .then((res) => {
        if (!signal.aborted && mountedRef.current) {
          setRecords(res.records ?? [])
          setError(null)
        }
      })
      .catch((err) => {
        if (!signal.aborted && mountedRef.current) {
          setError(err instanceof Error ? err.message : 'Failed to fetch recurring findings')
        }
      })
      .finally(() => {
        controllersRef.current.delete(abortController)
        if (!signal.aborted && mountedRef.current) {
          setLoading(false)
        }
      })

    return () => {
      abortController.abort()
      controllersRef.current.delete(abortController)
    }
  }, [minCount])

  useEffect(() => {
    mountedRef.current = true
    const controllers = controllersRef.current
    // eslint-disable-next-line react-hooks/set-state-in-effect -- starts async recurring-findings fetch/polling on mount; loading tracks external request lifecycle, not render-derived state.
    setLoading(true)
    const cleanup = refresh()
    const interval = setInterval(refresh, 20000)
    return () => {
      mountedRef.current = false
      cleanup?.()
      controllers.forEach((controller) => controller.abort())
      controllers.clear()
      clearInterval(interval)
    }
  }, [refresh])

  return { records, loading, error, refresh }
}

export function useMissionControlState() {
  const [state, setState] = useState<ControlState | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(false)
  const controllersRef = useRef<Set<AbortController>>(new Set())

  const refresh = useCallback(() => {
    const abortController = new AbortController()
    const signal = abortController.signal
    controllersRef.current.add(abortController)

    getControlState()
      .then((res) => {
        if (!signal.aborted && mountedRef.current) {
          setState(res)
          setError(null)
        }
      })
      .catch((err) => {
        if (!signal.aborted && mountedRef.current) {
          setError(err instanceof Error ? err.message : 'Failed to fetch control state')
        }
      })
      .finally(() => {
        controllersRef.current.delete(abortController)
        if (!signal.aborted && mountedRef.current) {
          setLoading(false)
        }
      })

    return () => {
      abortController.abort()
      controllersRef.current.delete(abortController)
    }
  }, [])

  useEffect(() => {
    mountedRef.current = true
    const controllers = controllersRef.current
    // eslint-disable-next-line react-hooks/set-state-in-effect -- starts async control-state fetch/polling on mount; loading tracks external request lifecycle, not render-derived state.
    setLoading(true)
    const cleanup = refresh()
    const interval = setInterval(refresh, 20000)
    return () => {
      mountedRef.current = false
      cleanup?.()
      controllers.forEach((controller) => controller.abort())
      controllers.clear()
      clearInterval(interval)
    }
  }, [refresh])

  return { state, loading, error, refresh }
}
