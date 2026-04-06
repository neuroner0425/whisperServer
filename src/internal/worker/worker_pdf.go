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

	intutil "whisperserver/src/internal/util"
)

type pdfChunkIndex struct {
	MaxPagesPerRequest int   `json:"max_pages_per_request"`
	TotalChunks        int   `json:"total_chunks"`
	PageCount          int   `json:"page_count"`
	LastCompletedChunk int   `json:"last_completed_chunk"`
	UpdatedAtUnix      int64 `json:"updated_at_unix"`
}

func (w *Worker) taskExtractPDF(jobID string) error {
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
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
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
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
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
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.IncJobsTotal("failure")
		return err
	}

	w.deps.SetJobFields(jobID, map[string]any{
		"phase":            "PDF 페이지 변환 중",
		"progress_percent": 5,
	})
	pageCount, err := w.deps.CountPDFPages(pdfPath)
	if err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_detail": "PDF 페이지 수 확인 실패"})
		w.deps.IncJobsTotal("failure")
		return err
	}
	if pageCount > w.cfg.PDFMaxPages {
		err = fmt.Errorf("pdf page limit exceeded: %d > %d", pageCount, w.cfg.PDFMaxPages)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_detail": fmt.Sprintf("PDF는 최대 %d페이지까지 지원합니다.", w.cfg.PDFMaxPages)})
		w.deps.IncJobsTotal("failure")
		return err
	}

	imagePaths, err := w.deps.RenderPDFToJPEGs(pdfPath, tmpDir)
	if err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_detail": "PDF 페이지 변환 실패"})
		w.deps.IncJobsTotal("failure")
		return err
	}
	if len(imagePaths) == 0 {
		err = errors.New("no rendered pages")
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_detail": "PDF 페이지가 없습니다."})
		w.deps.IncJobsTotal("failure")
		return err
	}
	if len(imagePaths) > w.cfg.PDFMaxPages {
		err = fmt.Errorf("pdf rendered page limit exceeded: %d > %d", len(imagePaths), w.cfg.PDFMaxPages)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_detail": fmt.Sprintf("PDF는 최대 %d페이지까지 지원합니다.", w.cfg.PDFMaxPages)})
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

	resumeState, err := w.loadResumeState(jobID, len(imagePaths), totalChunks)
	if err != nil {
		w.deps.Errf("pdf.loadResumeState", err, "job_id=%s", jobID)
		return err
	}
	if !resumeState.Valid && resumeState.LastCompletedChunk > 0 {
		w.clearPDFChunkBlobs(jobID)
		resumeState = pdfResumeState{}
	}

	chunkResults := make([][]byte, 0, totalChunks)
	if resumeState.LastCompletedChunk > 0 {
		for i := 1; i <= resumeState.LastCompletedChunk; i++ {
			b, loadErr := w.deps.BlobSvc.Load(jobID, pdfChunkJSONKind(i))
			if loadErr != nil {
				return loadErr
			}
			chunkResults = append(chunkResults, b)
		}
		w.deps.SetJobFields(jobID, map[string]any{
			"processed_page_count": processedPagesForChunk(resumeState.LastCompletedChunk, len(imagePaths), w.cfg.PDFMaxPagesPerRequest),
			"current_chunk":        resumeState.LastCompletedChunk,
			"resume_available":     true,
		})
	}

	var totalRenderedBytes int64
	for idx := resumeState.LastCompletedChunk; idx < len(chunks); idx++ {
		if updated := w.deps.GetJob(jobID); updated == nil || updated.IsTrashed {
			return nil
		}

		contextText := ""
		if idx > 0 && w.deps.BuildConsistencyContext != nil {
			merged, mergeErr := w.deps.MergeDocumentJSON(chunkResults...)
			if mergeErr != nil {
				return mergeErr
			}
			contextText, err = w.deps.BuildConsistencyContext(merged)
			if err != nil {
				return err
			}
		}

		images := make([]DocumentPageImage, 0, len(chunks[idx]))
		for pageOffset, path := range chunks[idx] {
			b, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			totalRenderedBytes += int64(len(b))
			if totalRenderedBytes > w.cfg.PDFMaxRenderedImageBytes {
				err = fmt.Errorf("rendered image bytes exceeded: %d > %d", totalRenderedBytes, w.cfg.PDFMaxRenderedImageBytes)
				w.markPDFChunkFailure(jobID, idx+1, totalChunks, idx*w.cfg.PDFMaxPagesPerRequest+1, minInt((idx+1)*w.cfg.PDFMaxPagesPerRequest, len(imagePaths)), len(imagePaths), resumeState.LastCompletedChunk > 0)
				w.deps.IncJobsTotal("failure")
				return err
			}
			images = append(images, DocumentPageImage{
				PageIndex: idx*w.cfg.PDFMaxPagesPerRequest + pageOffset + 1,
				MIMEType:  "image/jpeg",
				Data:      b,
			})
		}

		startPage := images[0].PageIndex
		endPage := images[len(images)-1].PageIndex
		processedPageCount := processedPagesForChunk(idx, len(imagePaths), w.cfg.PDFMaxPagesPerRequest)
		w.deps.SetJobFields(jobID, map[string]any{
			"phase":                fmt.Sprintf("문서 분석 중... %d/%d 배치", idx+1, totalChunks),
			"progress_percent":     progressForChunk(idx, totalChunks),
			"status_detail":        fmt.Sprintf("현재 %d/%d 페이지 처리 완료", processedPageCount, len(imagePaths)),
			"processed_page_count": processedPageCount,
			"current_chunk":        idx + 1,
			"total_chunks":         totalChunks,
			"resume_available":     idx > 0,
		})
		w.deps.ReplaceJobPreviewText(jobID, fmt.Sprintf("문서 분석 중...\n배치 %d/%d\n페이지 %d~%d", idx+1, totalChunks, startPage, endPage))

		batchCtx, batchCancel := context.WithTimeout(ctx, time.Duration(w.cfg.PDFBatchTimeoutSec)*time.Second)
		result, extractErr := w.deps.ExtractDocumentChunk(batchCtx, DocumentChunk{
			ChunkIndex:  idx + 1,
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
			w.markPDFChunkFailure(jobID, idx+1, totalChunks, startPage, endPage, len(imagePaths), idx > 0)
			w.deps.IncJobsTotal(statusLabel)
			return extractErr
		}

		if err := w.deps.BlobSvc.Save(jobID, pdfChunkJSONKind(idx+1), result); err != nil {
			return err
		}
		if err := w.deps.BlobSvc.Save(jobID, pdfChunkContextKind(idx+1), []byte(contextText)); err != nil {
			return err
		}
		chunkResults = append(chunkResults, result)
		if err := w.savePDFChunkIndex(jobID, pdfChunkIndex{
			MaxPagesPerRequest: w.cfg.PDFMaxPagesPerRequest,
			TotalChunks:        totalChunks,
			PageCount:          len(imagePaths),
			LastCompletedChunk: idx + 1,
			UpdatedAtUnix:      time.Now().Unix(),
		}); err != nil {
			return err
		}
		processedPageCount = processedPagesForChunk(idx+1, len(imagePaths), w.cfg.PDFMaxPagesPerRequest)
		w.deps.SetJobFields(jobID, map[string]any{
			"processed_page_count": processedPageCount,
			"resume_available":     idx+1 < totalChunks,
		})
	}

	w.deps.SetJobFields(jobID, map[string]any{
		"phase":            "문서 병합 중",
		"progress_percent": 92,
		"status_detail":    "",
	})
	mergedJSON, err := w.deps.MergeDocumentJSON(chunkResults...)
	if err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_detail": "문서 병합 실패"})
		w.deps.IncJobsTotal("failure")
		return err
	}

	w.deps.SetJobFields(jobID, map[string]any{
		"phase":            "Markdown 생성 중",
		"progress_percent": 97,
	})
	markdown, err := w.deps.RenderDocumentMarkdown(mergedJSON)
	if err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_detail": "Markdown 생성 실패"})
		w.deps.IncJobsTotal("failure")
		return err
	}

	if err := w.deps.BlobSvc.SaveDocumentJSON(jobID, mergedJSON); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.IncJobsTotal("failure")
		return err
	}
	if err := w.deps.BlobSvc.SaveDocumentMarkdown(jobID, []byte(markdown)); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.IncJobsTotal("failure")
		return err
	}
	w.deps.BlobSvc.DeletePreview(jobID)

	completed := time.Now()
	w.deps.IncJobsTotal("success")
	w.deps.ObserveJobDuration(completed.Sub(started).Seconds())
	w.deps.SetJobFields(jobID, map[string]any{
		"status":               w.cfg.StatusCompleted,
		"result":               "db://document_markdown",
		"preview_text":         "",
		"completed_at":         completed.Format("2006-01-02 15:04:05"),
		"completed_ts":         float64(completed.Unix()),
		"duration":             intutil.FormatSeconds(int(completed.Sub(started).Seconds())),
		"progress_percent":     100,
		"phase":                "완료",
		"status_detail":        "",
		"processed_page_count": len(imagePaths),
		"current_chunk":        totalChunks,
		"total_chunks":         totalChunks,
		"resume_available":     false,
	})
	w.deps.Logf("[PDF] done job_id=%s pages=%d", jobID, len(imagePaths))
	return nil
}

type pdfResumeState struct {
	Valid              bool
	LastCompletedChunk int
}

func (w *Worker) loadResumeState(jobID string, pageCount, totalChunks int) (pdfResumeState, error) {
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
	if idx.MaxPagesPerRequest != w.cfg.PDFMaxPagesPerRequest || idx.PageCount != pageCount || idx.TotalChunks != totalChunks {
		return pdfResumeState{Valid: false, LastCompletedChunk: idx.LastCompletedChunk}, nil
	}
	if idx.LastCompletedChunk <= 0 {
		return pdfResumeState{Valid: true}, nil
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
		return pdfResumeState{Valid: false, LastCompletedChunk: idx.LastCompletedChunk}, nil
	}
	return pdfResumeState{Valid: true, LastCompletedChunk: idx.LastCompletedChunk}, nil
}

func (w *Worker) markPDFChunkFailure(jobID string, chunkIndex, totalChunks, startPage, endPage, totalPages int, resumeAvailable bool) {
	processed := processedPagesForChunk(chunkIndex-1, totalPages, w.cfg.PDFMaxPagesPerRequest)
	w.deps.SetJobFields(jobID, map[string]any{
		"status":               w.cfg.StatusFailed,
		"phase":                fmt.Sprintf("실패: %d/%d 배치", chunkIndex, totalChunks),
		"current_chunk":        chunkIndex,
		"total_chunks":         totalChunks,
		"processed_page_count": processed,
		"resume_available":     resumeAvailable,
		"status_detail":        fmt.Sprintf("실패: %d/%d 배치 (%d~%d 페이지)", chunkIndex, totalChunks, startPage, endPage),
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
			w.deps.BlobSvc.Delete(jobID, kind)
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

func pdfChunkJSONKind(n int) string {
	return "document_chunk_" + strconv.Itoa(n) + "_json"
}

func pdfChunkContextKind(n int) string {
	return "document_chunk_" + strconv.Itoa(n) + "_context"
}

func splitPagePaths(paths []string, chunkSize int) [][]string {
	if len(paths) == 0 {
		return nil
	}
	out := make([][]string, 0, (len(paths)+chunkSize-1)/chunkSize)
	for start := 0; start < len(paths); start += chunkSize {
		end := start + chunkSize
		if end > len(paths) {
			end = len(paths)
		}
		out = append(out, paths[start:end])
	}
	return out
}

func progressForChunk(idx, total int) int {
	if total <= 0 {
		return 10
	}
	return 10 + int(float64(idx)/float64(total)*75)
}

func processedPagesForChunk(completedChunks, pageCount, chunkSize int) int {
	if completedChunks <= 0 {
		return 0
	}
	processed := completedChunks * chunkSize
	if processed > pageCount {
		return pageCount
	}
	return processed
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func hasContinuousChunkJSON(kinds map[string]struct{}, lastCompletedChunk int) bool {
	for i := 1; i <= lastCompletedChunk; i++ {
		if _, ok := kinds[pdfChunkJSONKind(i)]; !ok {
			return false
		}
	}
	return true
}
