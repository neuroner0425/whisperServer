import { memo, useEffect, useMemo, useRef, useState } from 'react'
import { Document, Page, pdfjs } from 'react-pdf'
import ReactMarkdown from 'react-markdown'
import rehypeKatex from 'rehype-katex'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'
import { useParams, useSearchParams } from 'react-router-dom'

import { fetchDocumentJSON, fetchJobDetail, refineJob, rerefineJob, retranscribeJob, retryJob } from './api'
import { buildJobStatusText } from './jobStatusText'
import type { DocumentListItem, DocumentPage, JobDetailResponse } from './types'
import { usePageTitle } from '../../usePageTitle'

pdfjs.GlobalWorkerOptions.workerSrc = new URL('pdfjs-dist/build/pdf.worker.min.mjs', import.meta.url).toString()

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

const PDFPreviewPage = memo(function PDFPreviewPage({
  isActive,
  onRef,
  pageNumber,
  width,
}: {
  isActive: boolean
  onRef: (node: HTMLDivElement | null) => void
  pageNumber: number
  width: number
}) {
  return (
    <div className={`pdf-page-shell${isActive ? ' active' : ''}`} data-page-number={pageNumber} ref={onRef}>
      <Page pageNumber={pageNumber} renderAnnotationLayer={false} renderTextLayer={false} width={width} />
    </div>
  )
})

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
  const [documentPages, setDocumentPages] = useState<DocumentPage[]>([])
  const [currentPDFPage, setCurrentPDFPage] = useState(1)
  const [pdfPageCount, setPDFPageCount] = useState(0)
  const [pdfRenderWidth, setPDFRenderWidth] = useState(760)
  const audioRef = useRef<HTMLAudioElement | null>(null)
  const itemRefs = useRef<Record<string, HTMLElement | null>>({})
  const pdfViewportRef = useRef<HTMLElement | null>(null)
  const pdfPageRefs = useRef<Record<number, HTMLDivElement | null>>({})
  const comparePageRefs = useRef<Record<number, HTMLElement | null>>({})

  const showOriginal = searchParams.get('original') === 'true'
  const showCompare = searchParams.get('compare') === 'true'
  const currentFileName = displayFilename(data?.job.Filename || '파일')
  const progressText = data ? buildJobStatusText(data.job) : ''
  const isPDF = data?.job.FileType === 'pdf'
  const isFailed = isFailureStatusCode(data?.job.StatusCode)

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

  useEffect(() => {
    if (!isPDF || data?.view !== 'result' || !data.download_document_json_url) {
      setDocumentPages([])
      setCurrentPDFPage(1)
      return
    }
    let closed = false
    const controller = new AbortController()
    void fetchDocumentJSON(data.download_document_json_url, controller.signal)
      .then((payload) => {
        if (closed) {
          return
        }
        const pages = [...(payload.pages || [])].sort((a, b) => a.page_index - b.page_index)
        setDocumentPages(pages)
        setCurrentPDFPage((current) => {
          if (pages.length === 0) {
            return 1
          }
          const pageIndexes = new Set(pages.map((page) => page.page_index))
          if (pageIndexes.has(current)) {
            return current
          }
          return pages[0].page_index
        })
      })
      .catch((loadError) => {
        if (!closed && !controller.signal.aborted) {
          setError(normalizeLoadError(loadError, '문서 JSON을 불러오지 못했습니다.'))
        }
      })
    return () => {
      closed = true
      controller.abort()
    }
  }, [data?.download_document_json_url, data?.view, isPDF])

  const toggleOriginal = (enabled: boolean) => {
    const next = new URLSearchParams(searchParams)
    if (enabled) {
      next.set('original', 'true')
    } else {
      next.delete('original')
    }
    setSearchParams(next)
  }

  const toggleCompare = (enabled: boolean) => {
    const next = new URLSearchParams(searchParams)
    if (enabled) {
      next.set('compare', 'true')
    } else {
      next.delete('compare')
    }
    setSearchParams(next)
  }

  const downloadHref = useMemo(() => {
    if (!data) {
      return ''
    }
    return data.has_refined && data.variant !== 'original' ? data.download_refined_url || data.download_text_url || '' : data.download_text_url || ''
  }, [data])

  const transcriptSourceJSON = useMemo(() => {
    if (!data) {
      return ''
    }
    if (data.view === 'result' && data.result_kind === 'transcript_json') {
      return data.result_json || ''
    }
    if (data.view === 'preview') {
      return data.original_json || ''
    }
    return ''
  }, [data])
  const transcriptSegments = useMemo(() => parseTranscriptSegmentsJSON(transcriptSourceJSON), [transcriptSourceJSON])
  const refinedParagraphs = useMemo(
    () => parseRefinedParagraphs(data?.view === 'result' && data?.result_kind === 'refined' ? data?.result_json || '' : ''),
    [data?.result_json, data?.result_kind, data?.view],
  )
  const pdfDocumentOptions = useMemo(() => ({ withCredentials: true }), [])
  const pdfPageNumbers = useMemo(() => Array.from({ length: pdfPageCount }, (_, index) => index + 1), [pdfPageCount])
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
  const fallbackText = useMemo(() => normalizePlainText(data?.preview_text || ''), [data?.preview_text])
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
    const container = pdfViewportRef.current
    if (!container) {
      return
    }

    const updateWidth = () => {
      const nextWidth = Math.max(320, Math.min(900, Math.floor(container.clientWidth - 40)))
      setPDFRenderWidth((current) => (current === nextWidth ? current : nextWidth))
    }

    updateWidth()
    const observer = new ResizeObserver(updateWidth)
    observer.observe(container)
    return () => {
      observer.disconnect()
    }
  }, [])

  useEffect(() => {
    const container = pdfViewportRef.current
    if (!container || pdfPageCount <= 0) {
      return
    }
    const ratios = new Map<number, number>()
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          const page = Number((entry.target as HTMLElement).dataset.pageNumber || '0')
          if (page > 0) {
            ratios.set(page, entry.isIntersecting ? entry.intersectionRatio : 0)
          }
        }
        let bestPage = currentPDFPage
        let bestRatio = 0
        for (const [page, ratio] of ratios.entries()) {
          if (ratio > bestRatio || (ratio === bestRatio && page < bestPage)) {
            bestPage = page
            bestRatio = ratio
          }
        }
        if (bestRatio > 0) {
          setCurrentPDFPage((current) => (current === bestPage ? current : bestPage))
        }
      },
      {
        root: container,
        threshold: [0.25, 0.5, 0.75],
      },
    )

    for (let pageNumber = 1; pageNumber <= pdfPageCount; pageNumber += 1) {
      const node = pdfPageRefs.current[pageNumber]
      if (node) {
        observer.observe(node)
      }
    }
    return () => {
      observer.disconnect()
    }
  }, [currentPDFPage, pdfPageCount])

  useEffect(() => {
    if (!showCompare) {
      return
    }
    const node = comparePageRefs.current[currentPDFPage]
    if (!node) {
      return
    }
    node.scrollIntoView({ block: 'nearest' })
  }, [currentPDFPage, showCompare])

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

  const scrollPDFToPage = (pageNumber: number) => {
    const node = pdfPageRefs.current[pageNumber]
    if (!node) {
      setCurrentPDFPage(pageNumber)
      return
    }
    node.scrollIntoView({ block: 'start', behavior: 'smooth' })
    setCurrentPDFPage(pageNumber)
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
          {isFailed ? (
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
                {isPDF && data.view === 'result' && data.original_pdf_url ? (
                  <button className={`ghost-button small${showCompare ? ' active' : ''}`} onClick={() => toggleCompare(!showCompare)} type="button">
                    대조 보기
                  </button>
                ) : null}
                {isPDF && data.view === 'result' && data.original_pdf_url ? <span className="pdf-page-indicator">현재 페이지 {currentPDFPage}</span> : null}
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
                {isPDF && (data.job.ResumeAvailable || data.resume_available) && isFailed ? (
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
              <section className={`pdf-reading-layout${showCompare && data.original_pdf_url ? ' compare' : ''}`}>
                <section className="result-panel dark pdf-source-panel" ref={pdfViewportRef}>
                  {data.original_pdf_url ? (
                    <Document
                      file={data.original_pdf_url}
                      loading={<div className="pdf-viewer-loading">PDF를 불러오는 중입니다.</div>}
                      onLoadError={(loadError) => {
                        setError(normalizeLoadError(loadError, 'PDF를 불러오지 못했습니다.'))
                      }}
                      options={pdfDocumentOptions}
                      onLoadSuccess={({ numPages }) => {
                        setPDFPageCount(numPages)
                        setCurrentPDFPage((current) => clampPage(current, numPages))
                      }}
                    >
                      {pdfPageNumbers.map((pageNumber) => (
                        <PDFPreviewPage
                          isActive={pageNumber === currentPDFPage}
                          key={pageNumber}
                          onRef={(node) => {
                            pdfPageRefs.current[pageNumber] = node
                          }}
                          pageNumber={pageNumber}
                          width={pdfRenderWidth}
                        />
                      ))}
                    </Document>
                  ) : (
                    <div className="pdf-viewer-loading">원본 PDF를 찾을 수 없습니다.</div>
                  )}
                </section>
                {showCompare && data.original_pdf_url ? (
                  <section className="result-panel dark markdown-panel compare-text-panel">
                    {documentPages.map((page) => (
                      <article
                        className={`compare-page-section${page.page_index === currentPDFPage ? ' active' : ''}`}
                        key={page.page_index}
                        onClick={() => scrollPDFToPage(page.page_index)}
                        onKeyDown={(event) => {
                          if (event.key === 'Enter' || event.key === ' ') {
                            event.preventDefault()
                            scrollPDFToPage(page.page_index)
                          }
                        }}
                        role="button"
                        ref={(node) => {
                          comparePageRefs.current[page.page_index] = node
                        }}
                        tabIndex={0}
                      >
                        <ReactMarkdown rehypePlugins={[rehypeKatex]} remarkPlugins={[remarkGfm, remarkMath]}>
                          {renderDocumentPageMarkdown(page)}
                        </ReactMarkdown>
                      </article>
                    ))}
                  </section>
                ) : null}
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

function parseTranscriptSegmentsJSON(raw: string): TimelineSegment[] {
  if (!raw.trim()) {
    return []
  }
  try {
    const parsed = JSON.parse(raw) as { segments?: Array<{ from?: string; to?: string; text?: string }> }
    return (parsed.segments || [])
      .map((segment, index) => {
        const startLabel = normalizeBracketTimestamp(segment.from || '')
        const endLabel = normalizeBracketTimestamp(segment.to || '')
        const content = (segment.text || '').trim()
        return {
          key: `segment-${index}`,
          startMs: parseTimelineMs(startLabel),
          endMs: parseTimelineMs(endLabel),
          timeLabel: `[${startLabel}]`,
          content,
        }
      })
      .filter((segment) => segment.content.length > 0)
  } catch {
    return []
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

function renderDocumentPageMarkdown(page: DocumentPage) {
  const lines: string[] = [`## Page ${page.page_index}`, '']
  for (const element of page.elements) {
    if (element.header) {
      const level = Math.max(1, Math.min(3, element.header.level || 1))
      lines.push(`${'#'.repeat(level)} ${element.header.text}`, '')
      continue
    }
    if (element.math_block?.trim()) {
      lines.push('$$', element.math_block.trim(), '$$', '')
      continue
    }
    if (element.math_inline?.trim()) {
      lines.push(`$${element.math_inline.trim()}$`, '')
      continue
    }
    if (element.text?.trim()) {
      lines.push(element.text.trim(), '')
      continue
    }
    if (element.list?.items?.length) {
      lines.push(...renderPageListMarkdown(element.list.items, 0))
      lines.push('')
      continue
    }
    if (element.code?.raw?.trim()) {
      const language = (element.code.languages || 'text').trim() || 'text'
      lines.push(`\`\`\`${language}`, element.code.raw.trimEnd(), '```', '')
      continue
    }
    if (element.img) {
      lines.push(`**${element.img.title}**`, '', element.img.description, '')
      continue
    }
    if (element.table) {
      lines.push(`**${element.table.title}**`, '')
      lines.push(...renderPageTableMarkdown(element.table.rows))
      lines.push('')
    }
  }
  return lines.join('\n').trim()
}

function renderPageTableMarkdown(rows: Array<{ cells: string[] }>) {
  if (!rows.length) {
    return []
  }
  const header = rows[0].cells
  const divider = header.map(() => '---')
  return [
    `| ${header.join(' | ')} |`,
    `| ${divider.join(' | ')} |`,
    ...rows.slice(1).map((row) => `| ${row.cells.join(' | ')} |`),
  ]
}

function renderPageListMarkdown(items: DocumentListItem[], depth: number): string[] {
  const lines: string[] = []
  for (const item of items) {
    const text = item.text?.trim()
    if (!text) {
      continue
    }
    const prefix = '  '.repeat(depth)
    lines.push(`${prefix}- ${text}`)
    if (item.children?.length) {
      lines.push(...renderPageListMarkdown(item.children, depth + 1))
    }
  }
  return lines
}

function clampPage(page: number, maxPage: number) {
  const upper = maxPage > 0 ? maxPage : 1
  if (page < 1) {
    return 1
  }
  if (page > upper) {
    return upper
  }
  return page
}

function isFailureStatusCode(statusCode?: number) {
  if (!statusCode) {
    return false
  }
  return statusCode >= 60
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
