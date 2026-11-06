import { useEffect, useRef, type ReactNode } from 'react'
import { SSEContext, type SSEEventListener } from './sseContextDef'

export function SSEProvider({ children }: { children: ReactNode }) {
  const listenersRef = useRef<Set<SSEEventListener>>(new Set())

  useEffect(() => {
    const source = new EventSource('/api/events')

    const handleUpdate = (event: Event) => {
      const msgEvent = event as MessageEvent<string>
      listenersRef.current.forEach((listener) => {
        try {
          listener(msgEvent)
        } catch {
          // ignore listener errors
        }
      })
    }

    source.addEventListener('update', handleUpdate)

    return () => {
      source.removeEventListener('update', handleUpdate)
      source.close()
    }
  }, [])

  const subscribe = (listener: SSEEventListener) => {
    listenersRef.current.add(listener)
    return () => {
      listenersRef.current.delete(listener)
    }
  }

  return (
    <SSEContext.Provider value={{ subscribe }}>
      {children}
    </SSEContext.Provider>
  )
}

