import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'

import { login, signup } from './api'
import { usePageTitle } from '../../usePageTitle'

type AuthPageProps = {
  mode: 'login' | 'signup'
}

export function AuthPage({ mode }: AuthPageProps) {
  usePageTitle(mode === 'login' ? 'Login' : 'Join')
  const navigate = useNavigate()
  const [error, setError] = useState('')
  const [isLoading, setIsLoading] = useState(false)
  const [identifier, setIdentifier] = useState('')
  const [loginId, setLoginId] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')

  const isLogin = mode === 'login'

  const handleSubmit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setIsLoading(true)
    setError('')
    try {
      if (isLogin) {
        await login({ identifier, password })
        navigate('/files/home')
      } else {
        await signup({ login_id: loginId, email, password })
        navigate('/auth/login')
      }
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : '요청에 실패했습니다.')
    } finally {
      setIsLoading(false)
    }
  }

  return (
    <div className="auth-shell">
      <section className="auth-panel">
        <div className="auth-copy">
          <p className="section-label">WHISPER DRIVE</p>
          <h1>{isLogin ? '작업 공간에 로그인' : '새 계정 만들기'}</h1>
          <p className="muted-text">
            {isLogin
              ? '오디오 파일과 전사 결과를 한 화면에서 정리하고 관리합니다.'
              : '계정을 만들면 업로드한 파일과 전사 결과를 개인 작업 공간에서 관리할 수 있습니다.'}
          </p>
        </div>

        <div className="auth-card">
          <div className="panel-header">
            <h2>{isLogin ? '로그인' : '회원가입'}</h2>
          </div>
          {error ? <div className="alert error">{error}</div> : null}
          <form className="stack-form auth-form" onSubmit={handleSubmit}>
            {isLogin ? (
              <div className="label-field">
                <label htmlFor="identifier">아이디 또는 이메일</label>
                <input
                  className="dark-input"
                  id="identifier"
                  onChange={(event) => setIdentifier(event.target.value)}
                  placeholder="아이디 또는 이메일"
                  value={identifier}
                />
              </div>
            ) : (
              <>
                <div className="label-field">
                  <label htmlFor="login-id">아이디</label>
                  <input
                    className="dark-input"
                    id="login-id"
                    onChange={(event) => setLoginId(event.target.value)}
                    placeholder="아이디 (3자 이상)"
                    value={loginId}
                  />
                </div>
                <div className="label-field">
                  <label htmlFor="email">이메일</label>
                  <input
                    className="dark-input"
                    id="email"
                    onChange={(event) => setEmail(event.target.value)}
                    placeholder="이메일"
                    type="email"
                    value={email}
                  />
                </div>
              </>
            )}
            <div className="label-field">
              <label htmlFor="password">비밀번호</label>
              <input
                className="dark-input"
                id="password"
                onChange={(event) => setPassword(event.target.value)}
                placeholder={isLogin ? '비밀번호' : '비밀번호 (8자 이상)'}
                type="password"
                value={password}
              />
            </div>
            <button className="primary-button full-width" disabled={isLoading} type="submit">
              {isLoading ? '처리 중...' : isLogin ? '로그인' : '회원가입'}
            </button>
          </form>
          <div className="auth-switch">
            <span>{isLogin ? '처음 오셨나요?' : '이미 계정이 있나요?'}</span>
            <Link to={isLogin ? '/auth/join' : '/auth/login'}>
              {isLogin ? '회원가입' : '로그인'}
            </Link>
          </div>
        </div>
      </section>
    </div>
  )
}
