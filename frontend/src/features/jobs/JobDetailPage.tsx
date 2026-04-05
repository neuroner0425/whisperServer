import { useEffect, useMemo, useRef, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import rehypeKatex from 'rehype-katex'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'
import { useParams, useSearchParams } from 'react-router-dom'

import { fetchJobDetail, refineJob, rerefineJob, retranscribeJob, retryJob } from './api'
import { buildJobStatusText } from './jobStatusText'
import type { JobDetailResponse } from './types'
import { usePageTitle } from '../../usePageTitle'

type TimelineSegment = {
  key: string
  startMs: number
  endMs: number
  timeLabel: string
  content: string
}

type RefinedParagraph = {
  key: string
  summary: string
  sentences: RefinedSentence[]
}

type RefinedSentence = {
  key: string
  startMs: number
  timeLabel: string
  content: string
}

type ActiveItem = {
  key: string
  startMs: number
  endMs: number
}

export function JobDetailPage() {
  const { jobId = '' } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const [data, setData] = useState<JobDetailResponse | null>(null)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [isLoading, setIsLoading] = useState(true)
  const [isRetrying, setIsRetrying] = useState(false)
  const [isRetranscribing, setIsRetranscribing] = useState(false)
  const [isRefining, setIsRefining] = useState(false)
  const [isRerefining, setIsRerefining] = useState(false)
  const [showMeta, setShowMeta] = useState(false)
  const [activeKey, setActiveKey] = useState('')
  const audioRef = useRef<HTMLAudioElement | null>(null)
  const itemRefs = useRef<Record<string, HTMLElement | null>>({})

  const showOriginal = searchParams.get('original') === 'true'
  const currentFileName = displayFilename(data?.job.Filename || '파일')
  const progressText = data ? buildJobStatusText(data.job) : ''
  const isPDF = data?.job.FileType === 'pdf'

  usePageTitle(currentFileName)

  useEffect(() => {
    let closed = false
    const controller = new AbortController()

    async function load(force: boolean) {
      if (force) {
        setIsLoading(true)
      }
      try {
        const payload = await fetchJobDetail(jobId, showOriginal, controller.signal)
        if (!closed) {
          setData(payload)
          setError('')
        }
      } catch (loadError) {
        if (!closed && !controller.signal.aborted) {
          setError(normalizeLoadError(loadError, '작업을 불러오지 못했습니다.'))
        }
      } finally {
        if (!closed && force) {
          setIsLoading(false)
        }
      }
    }

    void load(true)
    const shouldPoll = data?.view !== 'result'
    const timer = shouldPoll
      ? window.setInterval(() => {
          void load(false)
        }, 3000)
      : undefined

    return () => {
      closed = true
      controller.abort()
      if (timer) {
        window.clearInterval(timer)
      }
    }
  }, [jobId, showOriginal, data?.view])

  const toggleOriginal = (enabled: boolean) => {
    const next = new URLSearchParams(searchParams)
    if (enabled) {
      next.set('original', 'true')
    } else {
      next.delete('original')
    }
    setSearchParams(next)
  }

  const downloadHref = useMemo(() => {
    if (!data) {
      return ''
    }
    return data.has_refined && data.variant !== 'original' ? data.download_refined_url || data.download_text_url || '' : data.download_text_url || ''
  }, [data])

  const sourceText = useMemo(() => {
    if (!data) {
      return ''
    }
    if (data.view === 'result') {
      return data.text || ''
    }
    if (data.view === 'preview') {
      return data.original_text || data.preview_text || ''
    }
    return data.preview_text || ''
  }, [data])
  const transcriptSegments = useMemo(() => parseTimelineTranscriptText(sourceText), [sourceText])
  const refinedParagraphs = useMemo(() => parseRefinedParagraphs(data?.view === 'result' ? data?.text || '' : ''), [data?.text, data?.view])
  const activeItems = useMemo(() => {
    if (refinedParagraphs.length > 0 && !(data?.variant === 'original')) {
      return buildActiveItemsFromParagraphs(refinedParagraphs)
    }
    return transcriptSegments.map((segment) => ({
      key: segment.key,
      startMs: segment.startMs,
      endMs: segment.endMs,
    }))
  }, [data?.variant, refinedParagraphs, transcriptSegments])
  const fallbackText = useMemo(() => normalizePlainText(sourceText), [sourceText])
  useEffect(() => {
    if (!message && !error) {
      return
    }
    if (isPersistentNetworkError(error)) {
      return
    }
    const timer = window.setTimeout(() => {
      setMessage('')
      setError('')
    }, 2800)
    return () => window.clearTimeout(timer)
  }, [error, message])

  useEffect(() => {
    const audio = audioRef.current
    if (!audio || activeItems.length === 0) {
      setActiveKey('')
      return
    }

    const updateActive = () => {
      const currentMs = audio.currentTime * 1000
      const currentItem =
        activeItems.find((item) => currentMs >= item.startMs && currentMs < item.endMs) || activeItems[activeItems.length - 1] || null
      if (currentItem && currentMs >= currentItem.startMs) {
        setActiveKey(currentItem.key)
      } else {
        setActiveKey('')
      }
    }

    updateActive()
    audio.addEventListener('timeupdate', updateActive)
    audio.addEventListener('seeked', updateActive)
    audio.addEventListener('loadedmetadata', updateActive)
    return () => {
      audio.removeEventListener('timeupdate', updateActive)
      audio.removeEventListener('seeked', updateActive)
      audio.removeEventListener('loadedmetadata', updateActive)
    }
  }, [activeItems])

  useEffect(() => {
    if (!activeKey) {
      return
    }
    const node = itemRefs.current[activeKey]
    if (!node) {
      return
    }
    node.scrollIntoView({ block: 'nearest' })
  }, [activeKey])

  const seekTo = (startMs: number) => {
    const audio = audioRef.current
    if (!audio) {
      return
    }
    audio.currentTime = startMs / 1000
    void audio.play().catch(() => {})
  }

  const handleRetry = async () => {
    if (!jobId || isRetrying) {
      return
    }
    try {
      setIsRetrying(true)
      await retryJob(jobId)
      setMessage(isPDF && data?.resume_available ? '실패 배치부터 다시 처리합니다.' : isPDF ? '문서 처리를 다시 시작했습니다.' : '전사를 다시 시작했습니다.')
      const payload = await fetchJobDetail(jobId, showOriginal)
      setData(payload)
    } catch (retryError) {
      setError(normalizeLoadError(retryError, '재시도에 실패했습니다.'))
    } finally {
      setIsRetrying(false)
    }
  }

  const handleRefine = async () => {
    if (!jobId || isRefining) {
      return
    }
    try {
      setIsRefining(true)
      await refineJob(jobId)
      setMessage('정제를 시작했습니다.')
      const payload = await fetchJobDetail(jobId, showOriginal)
      setData(payload)
    } catch (refineError) {
      setError(normalizeLoadError(refineError, '정제를 시작하지 못했습니다.'))
    } finally {
      setIsRefining(false)
    }
  }

  const handleRetranscribe = async () => {
    if (!jobId || isRetranscribing) {
      return
    }
    try {
      setIsRetranscribing(true)
      const payload = await retranscribeJob(jobId)
      setMessage(payload.will_refine ? '전사를 다시 시작했고, 완료 후 정제도 다시 진행합니다.' : '전사를 다시 시작했습니다.')
      const next = new URLSearchParams(searchParams)
      next.delete('original')
      setSearchParams(next)
      const refreshed = await fetchJobDetail(jobId, false)
      setData(refreshed)
    } catch (retranscribeError) {
      setError(normalizeLoadError(retranscribeError, '전사를 다시 시작하지 못했습니다.'))
    } finally {
      setIsRetranscribing(false)
    }
  }

  const handleRerefine = async () => {
    if (!jobId || isRerefining) {
      return
    }
    try {
      setIsRerefining(true)
      await rerefineJob(jobId)
      setMessage('정제를 다시 시작했습니다.')
      const next = new URLSearchParams(searchParams)
      next.delete('original')
      setSearchParams(next)
      const refreshed = await fetchJobDetail(jobId, false)
      setData(refreshed)
    } catch (rerefineError) {
      setError(normalizeLoadError(rerefineError, '정제를 다시 시작하지 못했습니다.'))
    } finally {
      setIsRerefining(false)
    }
  }

  return (
    <section className="view-shell job-detail-view">
      <header className="view-header">
        <div>
          <p className="view-eyebrow">FILE</p>
          <h1 className="view-title">{currentFileName}</h1>
        </div>
        <div className="view-actions">
          {data?.download_document_json_url ? (
            <a className="ghost-button" href={data.download_document_json_url}>
              JSON
            </a>
          ) : null}
          {data?.status === '실패' ? (
            <button className="ghost-button" disabled={isRetrying} onClick={() => void handleRetry()} type="button">
              {isRetrying ? '재시도 중...' : isPDF ? '문서 다시 처리' : '재시도'}
            </button>
          ) : null}
          {downloadHref ? (
            <a className="primary-button" href={downloadHref}>
              다운로드
            </a>
          ) : null}
        </div>
      </header>

      {error ? <div className="alert error">{error}</div> : null}
      {message ? <div className="alert info">{message}</div> : null}
      {isLoading && !data ? <div className="empty-panel">파일 정보를 불러오는 중입니다.</div> : null}

      {data ? (
        <>
          <section className="detail-meta-section">
            <button className="detail-meta-toggle" onClick={() => setShowMeta((current) => !current)} type="button">
              {showMeta ? '상세 정보 숨기기' : '상세 정보 보기'}
            </button>
            {showMeta ? (
              <section className="detail-list plain">
                <div className="detail-row compact">
                  <span className="detail-label">상태</span>
                  <span className="detail-value">{buildJobStatusText(data.job)}</span>
                </div>
                <div className="detail-row compact">
                  <span className="detail-label">업로드</span>
                  <span className="detail-value">{data.job.UploadedAt || '-'}</span>
                </div>
                <div className="detail-row compact">
                  <span className="detail-label">길이</span>
                  <span className="detail-value">{data.job.MediaDuration || '-'}</span>
                </div>
                <div className="detail-row compact">
                  <span className="detail-label">계정</span>
                  <span className="detail-value">{data.current_user_name || '-'}</span>
                </div>
                {isPDF ? (
                  <>
                    <div className="detail-row compact">
                      <span className="detail-label">총 페이지</span>
                      <span className="detail-value">{data.job.PageCount || data.page_count || '-'}</span>
                    </div>
                    <div className="detail-row compact">
                      <span className="detail-label">완료 페이지</span>
                      <span className="detail-value">{data.job.ProcessedPageCount || data.processed_page_count || 0}</span>
                    </div>
                    <div className="detail-row compact">
                      <span className="detail-label">배치</span>
                      <span className="detail-value">
                        {(data.job.CurrentChunk || data.current_chunk || 0)}/{data.job.TotalChunks || data.total_chunks || 0}
                      </span>
                    </div>
                    <div className="detail-row compact">
                      <span className="detail-label">재개 가능</span>
                      <span className="detail-value">{data.job.ResumeAvailable || data.resume_available ? '예' : '아니오'}</span>
                    </div>
                  </>
                ) : null}
                <div className="detail-meta-actions">
                  {data.status === '완료' ? (
                    <button className="ghost-button small" disabled={isRetranscribing} onClick={() => void handleRetranscribe()} type="button">
                      {isRetranscribing ? (isPDF ? '문서 다시 처리 중...' : '전사 다시 시작 중...') : isPDF ? '문서 다시 처리' : '전사 다시하기'}
                    </button>
                  ) : null}
                  {data.status === '완료' && !isPDF && data.has_refined && data.can_refine ? (
                    <button className="ghost-button small" disabled={isRerefining} onClick={() => void handleRerefine()} type="button">
                      {isRerefining ? '정제 다시 시작 중...' : '정제 다시하기'}
                    </button>
                  ) : null}
                  {data.status === '완료' && !isPDF && !data.has_refined && data.can_refine ? (
                    <button className="ghost-button small" disabled={isRefining} onClick={() => void handleRefine()} type="button">
                      {isRefining ? '정제 시작 중...' : '정제하기'}
                    </button>
                  ) : null}
                </div>
              </section>
            ) : null}
          </section>

          <section className="detail-content-section">
            <div className="detail-content-header">
              <h2>파일 내용</h2>
              <div className="toolbar-actions">
                {data.has_refined ? (
                  <>
                    <button className={`ghost-button small${showOriginal ? '' : ' active'}`} onClick={() => toggleOriginal(false)} type="button">
                      정제본
                    </button>
                    <button className={`ghost-button small${showOriginal ? ' active' : ''}`} onClick={() => toggleOriginal(true)} type="button">
                      원본
                    </button>
                  </>
                ) : null}
              </div>
            </div>

            {data.audio_url ? (
              <div className="audio-player-shell">
                <audio controls preload="metadata" ref={audioRef} src={data.audio_url} />
              </div>
            ) : null}

            {data.view !== 'result' ? (
              <div className="progress-shell">
                <div className="progress-label">{progressText || '처리 중'}</div>
                <div className="progress-track dark">
                  <div className="progress-bar" style={{ width: `${data.job.ProgressPercent}%` }} />
                </div>
                {isPDF ? (
                  <div className="detail-row compact">
                    <span className="detail-value">
                      현재 {data.job.ProcessedPageCount || data.processed_page_count || 0}/{data.job.PageCount || data.page_count || 0} 페이지 처리 완료
                    </span>
                  </div>
                ) : null}
                {isPDF && (data.job.ResumeAvailable || data.resume_available) && data.status === '실패' ? (
                  <div className="detail-row compact">
                    <span className="detail-value">실패 배치부터 재시도할 수 있습니다.</span>
                  </div>
                ) : null}
              </div>
            ) : null}

            {refinedParagraphs.length > 0 && !(data.variant === 'original') ? (
              <section className="result-panel dark structured-panel">
                {refinedParagraphs.map((paragraph) => (
                  <article className="transcript-paragraph" key={paragraph.key}>
                    <p className="transcript-paragraph-summary">{paragraph.summary}</p>
                    <div className="transcript-prose">
                      {paragraph.sentences.map((sentence) => (
                        <span
                          className={`transcript-inline${activeKey === sentence.key ? ' active' : ''}`}
                          key={sentence.key}
                          onClick={() => seekTo(sentence.startMs)}
                          ref={(node) => {
                            itemRefs.current[sentence.key] = node
                          }}
                          onKeyDown={(event) => {
                            if (event.key === 'Enter' || event.key === ' ') {
                              event.preventDefault()
                              seekTo(sentence.startMs)
                            }
                          }}
                          role="button"
                          tabIndex={0}
                        >
                          {sentence.content}
                        </span>
                      ))}
                    </div>
                  </article>
                ))}
              </section>
            ) : transcriptSegments.length > 0 ? (
              <section className="result-panel dark structured-panel">
                <div className="transcript-prose">
                  {transcriptSegments.map((segment) => (
                    <span
                      className={`transcript-inline${activeKey === segment.key ? ' active' : ''}`}
                      key={segment.key}
                      onClick={() => seekTo(segment.startMs)}
                      ref={(node) => {
                        itemRefs.current[segment.key] = node
                      }}
                      onKeyDown={(event) => {
                        if (event.key === 'Enter' || event.key === ' ') {
                          event.preventDefault()
                          seekTo(segment.startMs)
                        }
                      }}
                      role="button"
                      tabIndex={0}
                    >
                      {segment.content}
                    </span>
                  ))}
                </div>
              </section>
            ) : isPDF && data.view === 'result' ? (
              <section className="result-panel dark markdown-panel">
                <ReactMarkdown rehypePlugins={[rehypeKatex]} remarkPlugins={[remarkGfm, remarkMath]}>
                  {sourceText}
                </ReactMarkdown>
              </section>
            ) : (
              <pre className="result-panel dark">{fallbackText}</pre>
            )}
          </section>
        </>
      ) : null}
    </section>
  )
}

function displayFilename(filename: string) {
  const dotIndex = filename.lastIndexOf('.')
  const ext = dotIndex > 0 ? filename.slice(dotIndex + 1).toLowerCase() : ''
  if (ext === 'mp3' || ext === 'wav' || ext === 'm4a') {
    return dotIndex > 0 ? filename.slice(0, dotIndex) : filename
  }
  return filename
}

function parseTimelineTranscriptText(text: string): TimelineSegment[] {
  if (!text.trim()) {
    return []
  }
  const lines = text.replace(/\r\n/g, '\n').split('\n')
  const segments: TimelineSegment[] = []
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index].trim()
    if (!line) {
      continue
    }
    const normalized = parseTimelineLine(line)
    if (!normalized) {
      continue
    }
    const { startLabel, endLabel, content } = normalized
    segments.push({
      key: `segment-${index}`,
      startMs: parseTimelineMs(startLabel),
      endMs: parseTimelineMs(endLabel),
      timeLabel: `[${startLabel}]`,
      content,
    })
  }
  return segments
}

function parseTimelineLine(line: string) {
  const tempStyle = line.match(/^(\d{2}:\d{2}:\d{2},\d{3})\s*~\s*(\d{2}:\d{2}:\d{2},\d{3})\s*"([\s\S]*)"$/)
  if (tempStyle) {
    return {
      startLabel: tempStyle[1],
      endLabel: tempStyle[2],
      content: tempStyle[3].trim(),
    }
  }
  const legacyStyle = line.match(/^\[(\d{2}:\d{2}:\d{2})\.(\d{3})\s*-->\s*(\d{2}:\d{2}:\d{2})\.(\d{3})\]\s*(.*)$/)
  if (!legacyStyle) {
    return null
  }
  return {
    startLabel: `${legacyStyle[1]},${legacyStyle[2]}`,
    endLabel: `${legacyStyle[3]},${legacyStyle[4]}`,
    content: legacyStyle[5].trim(),
  }
}

function parseRefinedParagraphs(text: string): RefinedParagraph[] {
  if (!text.trim()) {
    return []
  }
  try {
    const parsed = JSON.parse(text) as {
      paragraph?: Array<{
        paragraph_summary?: string
        sentence?: Array<{ start_time?: string; content?: string }>
      }>
    }
    const paragraphs = parsed.paragraph || []
    return paragraphs.map((paragraph, paragraphIndex) => ({
      key: `paragraph-${paragraphIndex}`,
      summary: (paragraph.paragraph_summary || '').trim(),
      sentences: (paragraph.sentence || [])
        .map((sentence, sentenceIndex) => {
          const timeLabel = normalizeBracketTimestamp(sentence.start_time || '')
          return {
            key: `paragraph-${paragraphIndex}-sentence-${sentenceIndex}`,
            startMs: parseTimelineMs(timeLabel),
            timeLabel: `[${timeLabel}]`,
            content: (sentence.content || '').trim(),
          }
        })
        .filter((sentence) => sentence.timeLabel !== '[]' && sentence.content.length > 0),
    }))
  } catch {
    return []
  }
}

function buildActiveItemsFromParagraphs(paragraphs: RefinedParagraph[]): ActiveItem[] {
  const flat = paragraphs.flatMap((paragraph) => paragraph.sentences)
  return flat.map((sentence, index) => ({
    key: sentence.key,
    startMs: sentence.startMs,
    endMs: flat[index + 1]?.startMs ?? Number.POSITIVE_INFINITY,
  }))
}

function parseTimelineMs(label: string) {
  const normalized = label.replace(/^\[|\]$/g, '')
  const match = normalized.match(/^(\d{2}):(\d{2}):(\d{2}),(\d{3})$/)
  if (!match) {
    return 0
  }
  const hours = Number(match[1])
  const minutes = Number(match[2])
  const seconds = Number(match[3])
  const milliseconds = Number(match[4])
  return ((hours * 60 * 60 + minutes * 60 + seconds) * 1000) + milliseconds
}

function normalizeBracketTimestamp(value: string) {
  return value.trim().replace(/^\[/, '').replace(/\]$/, '')
}

function normalizePlainText(text: string) {
  if (!text.trim()) {
    return ''
  }
  return text
    .replace(/\r\n/g, '\n')
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
    .join('\n')
}

function isPersistentNetworkError(error: string) {
  return error === 'Failed to fetch' || error.includes('서버와 연결할 수 없습니다.')
}

function normalizeLoadError(error: unknown, fallback: string) {
  if (error instanceof Error && error.message === 'Failed to fetch') {
    return '서버와 연결할 수 없습니다.'
  }
  return error instanceof Error ? error.message : fallback
}
