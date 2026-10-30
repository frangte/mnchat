import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, getToken, setToken, type User } from './api'

type AuthState = {
  user: User | null
  loading: boolean
  login: (userId: number) => Promise<void>
  logout: () => void
}

const AuthContext = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient()
  const [token, setTokenState] = useState(getToken)

  const me = useQuery({
    queryKey: ['me', token],
    queryFn: api.me,
    enabled: !!token,
    retry: false,
    staleTime: Infinity,
  })

  const logout = useCallback(() => {
    setToken(null)
    setTokenState(null)
    qc.clear()
  }, [qc])

  // Expired or invalid token: drop back to the login screen.
  useEffect(() => {
    if (me.error instanceof ApiError && me.error.status === 401) logout()
  }, [me.error, logout])

  const login = useCallback(
    async (userId: number) => {
      const res = await api.demoLogin(userId)
      setToken(res.token)
      qc.setQueryData(['me', res.token], res.user)
      setTokenState(res.token)
    },
    [qc],
  )

  const value = useMemo<AuthState>(
    () => ({
      user: token ? (me.data ?? null) : null,
      loading: !!token && me.isPending,
      login,
      logout,
    }),
    [token, me.data, me.isPending, login, logout],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used inside <AuthProvider>')
  return ctx
}
