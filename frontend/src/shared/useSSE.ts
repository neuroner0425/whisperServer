import { useContext, useEffect, useRef } from 'react'
import { SSEContext, type SSEEventListener } from './sseContextDef'

export function useSSE(listener: SSEEventListener) {
  const context = useContext(SSEContext)
  const listenerRef = useRef(listener)

  useEffect(() => {
    listenerRef.current = listener
  })

  useEffect(() => {
    if (!context) return
    const handler: SSEEventListener = (e) => listenerRef.current(e)
    return context.subscribe(handler)
  }, [context])
}
