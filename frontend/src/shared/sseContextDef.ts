import { createContext } from 'react'

export type SSEEventListener = (event: MessageEvent<string>) => void

export interface SSEContextValue {
  subscribe: (listener: SSEEventListener) => () => void
}

export const SSEContext = createContext<SSEContextValue | null>(null)

