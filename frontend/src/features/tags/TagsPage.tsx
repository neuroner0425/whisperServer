import { useEffect, useState } from 'react'

import { createTag, deleteTag, fetchTags } from './api'
import type { Tag } from './api'

export function TagsPage() {
  const [tags, setTags] = useState<Tag[]>([])
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [error, setError] = useState('')
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)

  const load = async () => {
    try {
      setTags(await fetchTags())
      setError('')
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : '태그를 불러오지 못했습니다.')
    }
  }

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void load()
    }, 0)
    return () => window.clearTimeout(timer)
  }, [])

  const handleCreate = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    try {
      await createTag(name, description)
      setName('')
      setDescription('')
      await load()
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : '태그를 저장하지 못했습니다.')
    }
  }

  const handleDelete = async (tagName: string) => {
    try {
      await deleteTag(tagName)
      setDeleteTarget(null)
      await load()
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : '태그를 삭제하지 못했습니다.')
    }
  }

  return (
    <section className="spa-section">
      <div className="content-header">
        <h2 className="content-title">태그 관리</h2>
      </div>
      {error ? <div className="error-banner">{error}</div> : null}
      <div className="content-filters">
        <form onSubmit={handleCreate} style={{ display: 'flex', gap: 8 }}>
          <input className="search-input-field" onChange={(event) => setName(event.target.value)} placeholder="새 태그 이름" value={name} />
          <input className="search-input-field" onChange={(event) => setDescription(event.target.value)} placeholder="설명" value={description} />
          <button className="search-submit-btn" type="submit">
            추가
          </button>
        </form>
      </div>
      <div className="fs-grid tag-grid">
        {tags.map((tag) => (
          <article className="entry-card align-start" key={tag.Name}>
            <div className="entry-title">🏷️ {tag.Name}</div>
            <div className="entry-sub">{tag.Description}</div>
            <button className="inline-link danger-link" onClick={() => setDeleteTarget(tag.Name)} type="button">
              삭제
            </button>
          </article>
        ))}
      </div>

      {deleteTarget ? (
        <div className="modal-shell">
          <div className="modal-card">
            <h3>태그 삭제</h3>
            <p className="modal-text">"{deleteTarget}" 태그를 삭제하시겠습니까?</p>
            <div className="modal-actions">
              <button className="ghost-button" onClick={() => setDeleteTarget(null)} type="button">
                취소
              </button>
              <button className="primary-button danger" onClick={() => void handleDelete(deleteTarget)} type="button">
                삭제
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </section>
  )
}
