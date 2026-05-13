// worker_pdf.go contains the PDF extraction pipeline used by the background worker.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	model "whisperserver/src/internal/domain"
	intutil "whisperserver/src/internal/util"
)

// pdfChunkIndex persists the resumable progress marker for PDF extraction.
type pdfChunkIndex struct {
	MaxPagesPerRequest int   `json:"max_pages_per_request"`
	PageCount          int   `json:"page_count"`
	LastCompletedChunk int   `json:"last_completed_chunk"`
	CompletedPages     int   `json:"completed_pages"`
	UpdatedAtUnix      int64 `json:"updated_at_unix"`
}

// taskExtractPDF renders pages, extracts chunk JSON, merges the document, and saves final blobs.
func (w *Worker) taskExtractPDF(jobID string) error {
	// Mark the job as running and load the original PDF blob into a temp workspace.
	w.deps.Logf("[PDF] start job_id=%s", jobID)
	started := time.Now()
	w.deps.SetJobFields(jobID, map[string]any{
		"status":               w.cfg.StatusRunning,
		"started_at":           started.Format("2006-01-02 15:04:05"),
		"started_ts":           float64(started.Unix()),
		"preview_text":         "",
		"progress_percent":     0,
		"phase":                "PDF 준비 중",
		"processed_page_count": 0,
		"current_chunk":        0,
		"resume_available":     false,
	})
	if w.deps.BlobSvc == nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusPDFExtractFailedCode})
		w.deps.IncJobsTotal("failure")
		return errors.New("missing blob service")
	}
	w.deps.BlobSvc.DeletePreview(jobID)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(w.cfg.JobTimeoutSec)*time.Second)
	w.setCancel(jobID, cancel)
	defer func() {
		cancel()
		w.setCancel(jobID, nil)
	}()

	pdfBytes, err := w.deps.BlobSvc.LoadPDFOriginal(jobID)
	if err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusPDFExtractFailedCode})
		w.deps.IncJobsTotal("failure")
		return err
	}

	tmpDir, err := os.MkdirTemp("", "pdf-job-*")
	if err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.IncJobsTotal("failure")
		return err
	}
	defer os.RemoveAll(tmpDir)

	pdfPath := filepath.Join(tmpDir, jobID+".pdf")
	if err := os.WriteFile(pdfPath, pdfBytes, 0o644); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusPDFConvertFailedCode})
		w.deps.IncJobsTotal("failure")
		return err
	}

	w.deps.SetJobFields(jobID, map[string]any{
		"phase":            "PDF 페이지 변환 중",
		"progress_percent": 5,
	})
	// Validate the page count and render each page into JPEG input for Gemini.
	pageCount, err := w.deps.CountPDFPages(pdfPath)
	if err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusPDFConvertFailedCode, "status_detail": "PDF 페이지 수 확인 실패"})
		w.deps.IncJobsTotal("failure")
		return err
	}
	if pageCount > w.cfg.PDFMaxPages {
		err = fmt.Errorf("pdf page limit exceeded: %d > %d", pageCount, w.cfg.PDFMaxPages)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusPDFConvertFailedCode, "status_detail": fmt.Sprintf("PDF는 최대 %d페이지까지 지원합니다.", w.cfg.PDFMaxPages)})
		w.deps.IncJobsTotal("failure")
		return err
	}
	if w.cfg.DevMode {
		return w.taskExtractPDFDev(jobID, started, pageCount)
	}

	imagePaths, err := w.deps.RenderPDFToJPEGs(pdfPath, tmpDir)
	if err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusPDFConvertFailedCode, "status_detail": "PDF 페이지 변환 실패"})
		w.deps.IncJobsTotal("failure")
		return err
	}
	if len(imagePaths) == 0 {
		err = errors.New("no rendered pages")
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusPDFConvertFailedCode, "status_detail": "PDF 페이지가 없습니다."})
		w.deps.IncJobsTotal("failure")
		return err
	}
	if len(imagePaths) > w.cfg.PDFMaxPages {
		err = fmt.Errorf("pdf rendered page limit exceeded: %d > %d", len(imagePaths), w.cfg.PDFMaxPages)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusPDFConvertFailedCode, "status_detail": fmt.Sprintf("PDF는 최대 %d페이지까지 지원합니다.", w.cfg.PDFMaxPages)})
		w.deps.IncJobsTotal("failure")
		return err
	}

	chunks := splitPagePaths(imagePaths, w.cfg.PDFMaxPagesPerRequest)
	totalChunks := len(chunks)
	w.deps.SetJobFields(jobID, map[string]any{
		"page_count":    len(imagePaths),
		"total_chunks":  totalChunks,
		"status_detail": fmt.Sprintf("총 %d페이지", len(imagePaths)),
	})

	resumeState, err := w.loadResumeState(jobID, len(imagePaths))
	if err != nil {
		w.deps.Errf("pdf.loadResumeState", err, "job_id=%s", jobID)
		return err
	}
	if !resumeState.Valid {
		resumeState = pdfResumeState{}
	}

	chunkResults := make([][]byte, 0, maxInt(totalChunks, resumeState.LastCompletedChunk))
	if resumeState.Valid && resumeState.LastCompletedChunk > 0 {
		for i := 1; i <= resumeState.LastCompletedChunk; i++ {
			b, loadErr := w.deps.BlobSvc.LoadBlob(jobID, pdfChunkJSONKind(i))
			if loadErr != nil {
				return loadErr
			}
			chunkResults = append(chunkResults, b)
		}
		w.deps.SetJobFields(jobID, map[string]any{
			"processed_page_count": resumeState.CompletedPages,
			"current_chunk":        resumeState.LastCompletedChunk,
			"resume_available":     true,
		})
	}

	var totalRenderedBytes int64
	completedPages := resumeState.CompletedPages
	successfulChunks := resumeState.LastCompletedChunk
	currentBatchSize := 0
	currentBatchFailures := 0
	lastFailureWasRecitation := false
	// Process the PDF in chunks so large files can resume and respect API limits.
	for completedPages < len(imagePaths) {
		if updated := w.deps.GetJob(jobID); updated == nil || updated.IsTrashed {
			return nil
		}

		remainingPaths := imagePaths[completedPages:]
		chunkPlan := buildChunkPlan(remainingPaths, w.cfg.PDFMaxPagesPerRequest, currentBatchSize)
		totalChunks = successfulChunks + len(chunkPlan)
		batchPaths := chunkPlan[0]
		startPage := completedPages + 1
		endPage := completedPages + len(batchPaths)
		contextText := ""
		if successfulChunks > 0 && w.deps.BuildConsistencyContext != nil {
			merged, mergeErr := w.deps.MergeDocumentJSON(chunkResults...)
			if mergeErr != nil {
				return mergeErr
			}
			contextText, err = w.deps.BuildConsistencyContext(merged)
			if err != nil {
				return err
			}
		}

		images := make([]DocumentPageImage, 0, len(batchPaths))
		for pageOffset, path := range batchPaths {
			b, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			totalRenderedBytes += int64(len(b))
			if totalRenderedBytes > w.cfg.PDFMaxRenderedImageBytes {
				err = fmt.Errorf("rendered image bytes exceeded: %d > %d", totalRenderedBytes, w.cfg.PDFMaxRenderedImageBytes)
				w.markPDFChunkFailure(jobID, successfulChunks+1, totalChunks, startPage, endPage, len(imagePaths), completedPages, successfulChunks > 0)
				w.deps.IncJobsTotal("failure")
				return err
			}
			images = append(images, DocumentPageImage{
				PageIndex: startPage + pageOffset,
				MIMEType:  "image/jpeg",
				Data:      b,
			})
		}
		processedPageCount := completedPages
		w.deps.SetJobFields(jobID, map[string]any{
			"phase":                fmt.Sprintf("문서 분석 중... %d/%d 배치", successfulChunks+1, totalChunks),
			"progress_percent":     progressForChunk(successfulChunks, totalChunks),
			"status_detail":        fmt.Sprintf("현재 %d/%d 페이지 처리 완료", processedPageCount, len(imagePaths)),
			"processed_page_count": processedPageCount,
			"current_chunk":        successfulChunks + 1,
			"total_chunks":         totalChunks,
			"resume_available":     successfulChunks > 0,
		})
		w.deps.ReplaceJobPreviewText(jobID, fmt.Sprintf("문서 분석 중...\n배치 %d/%d\n페이지 %d~%d", successfulChunks+1, totalChunks, startPage, endPage))

		batchCtx, batchCancel := context.WithTimeout(ctx, time.Duration(w.cfg.PDFBatchTimeoutSec)*time.Second)
		result, extractErr := w.deps.ExtractDocumentChunk(batchCtx, DocumentChunk{
			ChunkIndex:  successfulChunks + 1,
			TotalChunks: totalChunks,
			StartPage:   startPage,
			EndPage:     endPage,
			TotalPages:  len(imagePaths),
			Images:      images,
		}, contextText)
		batchCancel()
		if extractErr != nil {
			statusLabel := "failure"
			if errors.Is(extractErr, context.DeadlineExceeded) {
				statusLabel = "timeout"
			}
			lastFailureWasRecitation = isGeminiRecitationError(extractErr)
			currentBatchFailures++
			currentBatchSize = len(batchPaths)
			if currentBatchSize <= 5 || currentBatchFailures < 2 {
				w.markPDFChunkFailure(jobID, successfulChunks+1, totalChunks, startPage, endPage, len(imagePaths), completedPages, successfulChunks > 0)
				if currentBatchSize <= 5 {
					if lastFailureWasRecitation {
						w.markPDFCopyrightFailure(jobID, completedPages, successfulChunks, totalChunks)
					}
					w.deps.IncJobsTotal(statusLabel)
					return extractErr
				}
				w.deps.IncJobsTotal(statusLabel)
				continue
			}
			currentBatchSize = maxInt(1, currentBatchSize/2)
			currentBatchFailures = 0
			if currentBatchSize <= 5 {
				// One more failure at <=5 pages will terminate on the next iteration.
				w.markPDFChunkFailure(jobID, successfulChunks+1, totalChunks, startPage, minInt(startPage+currentBatchSize-1, len(imagePaths)), len(imagePaths), completedPages, successfulChunks > 0)
			}
			w.deps.IncJobsTotal(statusLabel)
			continue
		}

		if err := w.deps.BlobSvc.SaveBlob(jobID, pdfChunkJSONKind(successfulChunks+1), result); err != nil {
			return err
		}
		if err := w.deps.BlobSvc.SaveBlob(jobID, pdfChunkContextKind(successfulChunks+1), []byte(contextText)); err != nil {
			return err
		}
		chunkResults = append(chunkResults, result)
		if err := w.savePDFChunkIndex(jobID, pdfChunkIndex{
			MaxPagesPerRequest: w.cfg.PDFMaxPagesPerRequest,
			PageCount:          len(imagePaths),
			LastCompletedChunk: successfulChunks + 1,
			CompletedPages:     completedPages + len(batchPaths),
			UpdatedAtUnix:      time.Now().Unix(),
		}); err != nil {
			return err
		}
		completedPages += len(batchPaths)
		successfulChunks++
		currentBatchFailures = 0
		currentBatchSize = 0
		processedPageCount = completedPages
		w.deps.SetJobFields(jobID, map[string]any{
			"processed_page_count": processedPageCount,
			"current_chunk":        successfulChunks,
			"total_chunks":         totalChunks,
			"resume_available":     completedPages < len(imagePaths),
		})
	}

	// Merge chunk JSON into the final structured document result.
	w.deps.SetJobFields(jobID, map[string]any{
		"phase":            "문서 병합 중",
		"progress_percent": 92,
		"status_detail":    "",
	})
	mergedJSON, err := w.deps.MergeDocumentJSON(chunkResults...)
	if err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusPDFExtractFailedCode, "status_detail": "문서 병합 실패"})
		w.deps.IncJobsTotal("failure")
		return err
	}

	if err := w.deps.BlobSvc.SaveDocumentJSON(jobID, mergedJSON); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusPDFExtractFailedCode})
		w.deps.IncJobsTotal("failure")
		return err
	}
	w.deps.BlobSvc.DeletePreview(jobID)

	completed := time.Now()
	w.deps.IncJobsTotal("success")
	w.deps.ObserveJobDuration(completed.Sub(started).Seconds())
	w.deps.SetJobFields(jobID, map[string]any{
		"status":               w.cfg.StatusCompleted,
		"status_code":          model.JobStatusCompletedCode,
		"result":               "db://document_json",
		"preview_text":         "",
		"completed_at":         completed.Format("2006-01-02 15:04:05"),
		"completed_ts":         float64(completed.Unix()),
		"duration":             intutil.FormatSeconds(int(completed.Sub(started).Seconds())),
		"progress_percent":     100,
		"phase":                "완료",
		"status_detail":        "",
		"processed_page_count": len(imagePaths),
		"current_chunk":        successfulChunks,
		"total_chunks":         totalChunks,
		"resume_available":     false,
	})
	w.deps.Logf("[PDF] done job_id=%s pages=%d", jobID, len(imagePaths))
	return nil
}

// pdfResumeState is the parsed and validated PDF resume marker.
type pdfResumeState struct {
	Valid              bool
	LastCompletedChunk int
	CompletedPages     int
}

func (w *Worker) loadResumeState(jobID string, pageCount int) (pdfResumeState, error) {
	if w.deps.BlobSvc == nil {
		return pdfResumeState{}, nil
	}
	b, err := w.deps.BlobSvc.LoadDocumentChunkIndex(jobID)
	if err != nil {
		return pdfResumeState{}, nil
	}
	var idx pdfChunkIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return pdfResumeState{}, err
	}
	if idx.PageCount != pageCount {
		return pdfResumeState{Valid: false, LastCompletedChunk: idx.LastCompletedChunk, CompletedPages: idx.CompletedPages}, nil
	}
	if idx.LastCompletedChunk <= 0 {
		return pdfResumeState{Valid: true, CompletedPages: idx.CompletedPages}, nil
	}
	kinds, err := w.deps.BlobSvc.ListKinds(jobID)
	if err != nil {
		return pdfResumeState{}, err
	}
	available := map[string]struct{}{}
	for _, kind := range kinds {
		available[kind] = struct{}{}
	}
	if !hasContinuousChunkJSON(available, idx.LastCompletedChunk) {
		return pdfResumeState{Valid: false, LastCompletedChunk: idx.LastCompletedChunk, CompletedPages: idx.CompletedPages}, nil
	}
	completedPages := idx.CompletedPages
	if completedPages <= 0 {
		completedPages = deriveCompletedPagesFromLegacyIndex(idx, pageCount)
	}
	return pdfResumeState{Valid: true, LastCompletedChunk: idx.LastCompletedChunk, CompletedPages: completedPages}, nil
}

func (w *Worker) markPDFChunkFailure(jobID string, chunkIndex, totalChunks, startPage, endPage, totalPages int, completedPages int, resumeAvailable bool) {
	w.deps.SetJobFields(jobID, map[string]any{
		"status":               w.cfg.StatusFailed,
		"status_code":          model.JobStatusPDFExtractFailedCode,
		"phase":                fmt.Sprintf("실패: %d/%d 배치", chunkIndex, totalChunks),
		"current_chunk":        chunkIndex,
		"total_chunks":         totalChunks,
		"processed_page_count": completedPages,
		"resume_available":     resumeAvailable,
		"status_detail":        fmt.Sprintf("실패: %d/%d 배치 (%d~%d 페이지)", chunkIndex, totalChunks, startPage, endPage),
	})
}

func (w *Worker) markPDFCopyrightFailure(jobID string, completedPages, currentChunk, totalChunks int) {
	w.deps.SetJobFields(jobID, map[string]any{
		"status":               w.cfg.StatusFailed,
		"status_code":          model.JobStatusCopyrightBlockedCode,
		"phase":                "저작권 문제로 추출 불가",
		"status_detail":        "저작권 문제로 추출 불가",
		"processed_page_count": completedPages,
		"current_chunk":        currentChunk,
		"total_chunks":         totalChunks,
		"resume_available":     completedPages > 0,
	})
}

func (w *Worker) clearPDFChunkBlobs(jobID string) {
	if w.deps.BlobSvc == nil {
		return
	}
	kinds, err := w.deps.BlobSvc.ListKinds(jobID)
	if err != nil {
		return
	}
	for _, kind := range kinds {
		if kind == w.deps.BlobSvc.DocumentChunkIndexKind() || strings.HasPrefix(kind, "document_chunk_") {
			w.deps.BlobSvc.DeleteBlob(jobID, kind)
		}
	}
}

func (w *Worker) savePDFChunkIndex(jobID string, idx pdfChunkIndex) error {
	b, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	if w.deps.BlobSvc == nil {
		return errors.New("missing blob service")
	}
	return w.deps.BlobSvc.SaveDocumentChunkIndex(jobID, b)
}

// pdfChunkJSONKind returns the blob kind for one chunk JSON result.
func pdfChunkJSONKind(n int) string {
	return "document_chunk_" + strconv.Itoa(n) + "_json"
}

// pdfChunkContextKind returns the blob kind for one chunk consistency context.
func pdfChunkContextKind(n int) string {
	return "document_chunk_" + strconv.Itoa(n) + "_context"
}

// splitPagePaths splits rendered page paths into evenly sized batches with a hard upper bound.
func splitPagePaths(paths []string, maxChunkSize int) [][]string {
	if len(paths) == 0 {
		return nil
	}
	if maxChunkSize <= 0 {
		return [][]string{paths}
	}
	chunkCount := (len(paths) + maxChunkSize - 1) / maxChunkSize
	baseSize := len(paths) / chunkCount
	remainder := len(paths) % chunkCount
	out := make([][]string, 0, chunkCount)
	start := 0
	for i := 0; i < chunkCount; i++ {
		size := baseSize
		if i < remainder {
			size++
		}
		end := start + size
		out = append(out, paths[start:end])
		start = end
	}
	return out
}

func buildChunkPlan(paths []string, maxChunkSize int, firstChunkSize int) [][]string {
	if len(paths) == 0 {
		return nil
	}
	if firstChunkSize > 0 {
		if firstChunkSize > len(paths) {
			firstChunkSize = len(paths)
		}
		first := append([]string(nil), paths[:firstChunkSize]...)
		if firstChunkSize == len(paths) {
			return [][]string{first}
		}
		rest := splitPagePaths(paths[firstChunkSize:], maxChunkSize)
		return append([][]string{first}, rest...)
	}
	return splitPagePaths(paths, maxChunkSize)
}

// progressForChunk converts chunk progress into a coarse job percent.
func progressForChunk(idx, total int) int {
	if total <= 0 {
		return 10
	}
	return 10 + int(float64(idx)/float64(total)*75)
}

// processedPagesForChunk returns the number of pages covered by completed chunks.
func processedPagesForChunk(completedChunks int, chunks [][]string) int {
	if completedChunks <= 0 {
		return 0
	}
	if completedChunks > len(chunks) {
		completedChunks = len(chunks)
	}
	processed := 0
	for i := 0; i < completedChunks; i++ {
		processed += len(chunks[i])
	}
	return processed
}

func deriveCompletedPagesFromLegacyIndex(idx pdfChunkIndex, pageCount int) int {
	if idx.LastCompletedChunk <= 0 || idx.MaxPagesPerRequest <= 0 || pageCount <= 0 {
		return 0
	}
	placeholder := make([]string, pageCount)
	chunks := splitPagePaths(placeholder, idx.MaxPagesPerRequest)
	return processedPagesForChunk(idx.LastCompletedChunk, chunks)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// hasContinuousChunkJSON checks that chunk JSON blobs exist without gaps up to the marker.
func hasContinuousChunkJSON(kinds map[string]struct{}, lastCompletedChunk int) bool {
	for i := 1; i <= lastCompletedChunk; i++ {
		if _, ok := kinds[pdfChunkJSONKind(i)]; !ok {
			return false
		}
	}
	return true
}

func isGeminiRecitationError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToUpper(err.Error())
	return strings.Contains(msg, "FINISH_REASON=RECITATION") || strings.Contains(msg, "RECITATION")
}

func (w *Worker) taskExtractPDFDev(jobID string, started time.Time, pageCount int) error {
	w.deps.Logf("[PDF] dev stub start job_id=%s pages=%d", jobID, pageCount)
	if pageCount <= 0 {
		pageCount = 1
	}
	pages := make([]map[string]any, 0, pageCount)
	for i := 1; i <= pageCount; i++ {
		pages = append(pages, map[string]any{
			"page_index": i,
			"elements": []map[string]any{
				{
					"header": map[string]any{
						"level": 1,
						"text":  fmt.Sprintf("DEV PDF 테스트 페이지 %d", i),
					},
				},
				{
					"text": fmt.Sprintf("DEV 모드에서 생성한 PDF %d페이지 테스트 결과입니다. 실제 Gemini 문서 추출은 실행하지 않았습니다.", i),
				},
			},
		})
	}
	payload := map[string]any{"pages": pages}
	mergedJSON, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusPDFExtractFailedCode})
		w.deps.IncJobsTotal("failure")
		return err
	}
	if err := w.deps.BlobSvc.SaveDocumentJSON(jobID, mergedJSON); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusPDFExtractFailedCode})
		w.deps.IncJobsTotal("failure")
		return err
	}
	w.deps.BlobSvc.DeletePreview(jobID)

	completed := time.Now()
	w.deps.IncJobsTotal("success")
	w.deps.ObserveJobDuration(completed.Sub(started).Seconds())
	w.deps.SetJobFields(jobID, map[string]any{
		"status":               w.cfg.StatusCompleted,
		"status_code":          model.JobStatusCompletedCode,
		"result":               "db://document_json",
		"preview_text":         "",
		"completed_at":         completed.Format("2006-01-02 15:04:05"),
		"completed_ts":         float64(completed.Unix()),
		"duration":             intutil.FormatSeconds(int(completed.Sub(started).Seconds())),
		"progress_percent":     100,
		"phase":                "완료",
		"status_detail":        "",
		"page_count":           pageCount,
		"processed_page_count": pageCount,
		"current_chunk":        1,
		"total_chunks":         1,
		"resume_available":     false,
	})
	w.deps.Logf("[PDF] dev stub done job_id=%s pages=%d", jobID, pageCount)
	return nil
}
