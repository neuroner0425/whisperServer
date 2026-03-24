type PlaceholderProps = {
  title: string
  description: string
}

export function OverviewPage() {
  return (
    <section className="panel files-panel">
      <div>
        <h2>홈</h2>
        <p className="subtle">최근 작업과 주요 화면으로 이동할 수 있습니다.</p>
      </div>
      <div className="card-grid">
        <article className="card">
          <h3>내 파일</h3>
          <p>업로드한 파일, 폴더, 검색 결과를 한 곳에서 관리합니다.</p>
        </article>
        <article className="card">
          <h3>작업 상세</h3>
          <p>대기 중, 미리보기, 완료 상태를 같은 화면에서 확인할 수 있습니다.</p>
        </article>
      </div>
    </section>
  )
}

export function PlaceholderPage({ title, description }: PlaceholderProps) {
  return (
    <section className="panel files-panel">
      <h2>{title}</h2>
      <p className="subtle">{description}</p>
    </section>
  )
}
