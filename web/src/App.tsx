import { useAuth } from './auth'
import { LoginPage } from './pages/LoginPage'
import { ChatPage } from './pages/ChatPage'
import { Spinner } from './components/Spinner'

export default function App() {
  const { user, loading } = useAuth()
  if (loading) {
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner />
      </div>
    )
  }
  return user ? <ChatPage user={user} /> : <LoginPage />
}
