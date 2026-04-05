import { isRouteErrorResponse, useRouteError } from 'react-router-dom'

export function AppErrorPage() {
  const error = useRouteError()
  const title = isRouteErrorResponse(error) ? `${error.status} ${error.statusText}` : '화면을 불러오지 못했습니다.'
  const detail =
    isRouteErrorResponse(error)
      ? typeof error.data === 'string'
        ? error.data
        : '요청 처리 중 문제가 발생했습니다.'
      : error instanceof Error
        ? error.message
        : '잠시 후 다시 시도해 주세요.'

  return (
    <section className="app-error-shell">
      <div className="app-error-card">
        <p className="view-eyebrow">ERROR</p>
        <h1 className="view-title">{title}</h1>
        <p className="view-description">{detail}</p>
        <div className="view-actions">
          <a className="primary-button" href="/files/home">
            파일 홈으로
          </a>
        </div>
      </div>
    </section>
  )
}
