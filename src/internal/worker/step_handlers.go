package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/gemini"
	"whisperserver/src/internal/integrations/storage"
	intwhisper "whisperserver/src/internal/integrations/whisper"
	"whisperserver/src/internal/service"
	intutil "whisperserver/src/internal/util"
)

// AVScanStepHandler performs initial streaming malware scanning on uploaded files.
type AVScanStepHandler struct {
	Scanner domain.MalwareScanner
	Storage storage.Storage
	Logf    func(string, ...any)
	Errf    func(string, error, string, ...any)
}

func (h *AVScanStepHandler) Execute(ctx context.Context, task *domain.Task) (domain.StepResult, error) {
	var payload struct {
		StorageKey string `json:"storage_key"`
		TempPath   string `json:"temp_path"`
		FileType   string `json:"file_type"`
		Language   string `json:"language"`
	}
	_ = json.Unmarshal([]byte(task.PayloadJSON), &payload)

	var (
		res domain.ScanResult
		err error
	)

	// Direct storage streaming scan path (Phase 2 Presigned)
	if payload.StorageKey != "" && h.Storage != nil {
		rc, info, getErr := h.Storage.GetStream(ctx, payload.StorageKey)
		if getErr != nil {
			return domain.StepResult{}, fmt.Errorf("open quarantine stream for av scan failed: %w", getErr)
		}

		res, err = h.Scanner.ScanStream(ctx, rc, info.Size)
		_ = rc.Close()
		if err != nil {
			return domain.StepResult{}, fmt.Errorf("malware scanner failed: %w", err)
		}

		if res.Verdict == domain.VerdictInfected {
			_ = h.Storage.Delete(ctx, payload.StorageKey)
			return domain.StepResult{}, fmt.Errorf("%w (signature: %s)", ErrInfectedMalware, res.Signature)
		}

		// Promote quarantine_raw to source_raw
		sourceKey := fmt.Sprintf("jobs/%s/source_raw", task.JobID)
		srcRC, srcInfo, srcErr := h.Storage.GetStream(ctx, payload.StorageKey)
		if srcErr != nil {
			return domain.StepResult{}, fmt.Errorf("open quarantine stream for promotion failed: %w", srcErr)
		}
		_, putErr := h.Storage.PutStream(ctx, sourceKey, srcRC, srcInfo.Size, srcInfo.ContentType)
		_ = srcRC.Close()
		if putErr != nil {
			return domain.StepResult{}, fmt.Errorf("promote quarantine to source_raw failed: %w", putErr)
		}
		_ = h.Storage.Delete(ctx, payload.StorageKey)

		// If PDF, also store under pdf_original for downstream PDF steps
		if payload.FileType == "pdf" {
			pdfRC, pdfInfo, pErr := h.Storage.GetStream(ctx, sourceKey)
			if pErr != nil {
				return domain.StepResult{}, fmt.Errorf("open source stream for pdf promotion failed: %w", pErr)
			}
			pdfKey := fmt.Sprintf("jobs/%s/pdf_original", task.JobID)
			_, putPdfErr := h.Storage.PutStream(ctx, pdfKey, pdfRC, pdfInfo.Size, pdfInfo.ContentType)
			_ = pdfRC.Close()
			if putPdfErr != nil {
				return domain.StepResult{}, fmt.Errorf("promote source to pdf_original failed: %w", putPdfErr)
			}
		}
	} else if payload.TempPath != "" {
		// Legacy local file path fallback
		f, openErr := os.Open(payload.TempPath)
		if openErr != nil {
			return domain.StepResult{}, fmt.Errorf("open upload file for av scan failed: %w", openErr)
		}

		fi, statErr := f.Stat()
		if statErr != nil {
			_ = f.Close()
			return domain.StepResult{}, fmt.Errorf("stat upload file failed: %w", statErr)
		}

		res, err = h.Scanner.ScanStream(ctx, f, fi.Size())
		_ = f.Close()
		if err != nil {
			return domain.StepResult{}, fmt.Errorf("malware scanner failed: %w", err)
		}

		if res.Verdict == domain.VerdictInfected {
			_ = os.Remove(payload.TempPath)
			return domain.StepResult{}, fmt.Errorf("%w (signature: %s)", ErrInfectedMalware, res.Signature)
		}
	} else {
		return domain.StepResult{}, fmt.Errorf("no storage_key or temp_path provided for av scan")
	}

	qualityJSON, _ := json.Marshal(map[string]any{
		"clean":     true,
		"engine":    res.EngineVer,
		"signature": res.Signature,
	})

	var nextStep domain.StepType
	var nextClass domain.ResourceClass
	if payload.FileType == "pdf" {
		nextStep = domain.StepPDFRender
		nextClass = domain.ResourceClassMedia
	} else {
		nextStep = domain.StepNormalize
		nextClass = domain.ResourceClassMedia
	}

	nextPayloadMap := map[string]any{}
	_ = json.Unmarshal([]byte(task.PayloadJSON), &nextPayloadMap)
	nextPayloadMap["file_type"] = payload.FileType
	nextPayloadMap["language"] = payload.Language
	if payload.TempPath != "" {
		nextPayloadMap["temp_path"] = payload.TempPath
	}
	nextPayloadJSON, _ := json.Marshal(nextPayloadMap)

	return domain.StepResult{
		NextStep:      &nextStep,
		NextClass:     &nextClass,
		NextPayload:   string(nextPayloadJSON),
		QualityReport: string(qualityJSON),
	}, nil
}

// NormalizeStepHandler converts raw audio into web playback AAC and STT-ready WAV using hardware acceleration.
type NormalizeStepHandler struct {
	BlobSvc          *service.JobBlobService
	Storage          storage.Storage
	TmpFolder        string
	ConvertToAac     func(string, string) error
	ConvertToWav     func(string, string) error
	GetMediaDuration func(string) *int
	Logf             func(string, ...any)
	Errf             func(string, error, string, ...any)
}

func (h *NormalizeStepHandler) Execute(ctx context.Context, task *domain.Task) (domain.StepResult, error) {
	var payload struct {
		TempPath string `json:"temp_path"`
		Language string `json:"language"`
	}
	_ = json.Unmarshal([]byte(task.PayloadJSON), &payload)

	sourceKey := fmt.Sprintf("jobs/%s/source_raw", task.JobID)
	hasSourceRaw := false
	if h.Storage != nil {
		hasSourceRaw, _ = h.Storage.Exists(ctx, sourceKey)
	}

	var (
		qualityMap  = map[string]any{}
		durationSec *int
	)

	if hasSourceRaw && h.Storage != nil {
		// Phase 2: Single-pass hardware accelerated conversion from preserved source_raw
		workDir := h.TmpFolder
		if workDir == "" {
			workDir = os.TempDir()
		}
		rawInputPath := filepath.Join(workDir, task.JobID+"_source_raw")
		outWavPath := filepath.Join(workDir, task.JobID+"_temp_stt.wav")
		outAACPath := filepath.Join(workDir, task.JobID+"_playback.m4a")

		defer func() {
			_ = os.Remove(rawInputPath)
			_ = os.Remove(outWavPath)
			_ = os.Remove(outAACPath)
		}()

		// 1. Stream download source_raw
		srcRC, _, err := h.Storage.GetStream(ctx, sourceKey)
		if err != nil {
			return domain.StepResult{}, fmt.Errorf("download source_raw failed: %w", err)
		}
		rawF, err := os.Create(rawInputPath)
		if err != nil {
			_ = srcRC.Close()
			return domain.StepResult{}, fmt.Errorf("create raw temp file failed: %w", err)
		}
		_, copyErr := io.Copy(rawF, srcRC)
		_ = srcRC.Close()
		_ = rawF.Close()
		if copyErr != nil {
			return domain.StepResult{}, fmt.Errorf("stream copy source_raw failed: %w", copyErr)
		}

		if h.GetMediaDuration != nil {
			durationSec = h.GetMediaDuration(rawInputPath)
		}

		// 2. Single-pass dual conversion with hardware acceleration & fallback
		hwReport, err := intutil.ExtractSTTWavAndPlaybackAAC(ctx, rawInputPath, outWavPath, outAACPath)
		if err != nil {
			return domain.StepResult{}, fmt.Errorf("dual media conversion failed: %w", err)
		}

		// 3. Upload playback AAC
		aacF, err := os.Open(outAACPath)
		if err == nil {
			fi, _ := aacF.Stat()
			aacKey := fmt.Sprintf("jobs/%s/audio_aac", task.JobID)
			_, _ = h.Storage.PutStream(ctx, aacKey, aacF, fi.Size(), "audio/mp4")
			_ = aacF.Close()
		}
		if h.Storage == nil && h.BlobSvc != nil {
			if aacBytes, readErr := os.ReadFile(outAACPath); readErr == nil {
				_ = h.BlobSvc.SaveBlob(task.JobID, "audio_aac", aacBytes)
			}
		}

		// 4. Upload temporary STT WAV
		wavF, err := os.Open(outWavPath)
		if err == nil {
			fi, _ := wavF.Stat()
			wavKey := fmt.Sprintf("jobs/%s/temp_stt.wav", task.JobID)
			_, _ = h.Storage.PutStream(ctx, wavKey, wavF, fi.Size(), "audio/wav")
			_ = wavF.Close()
		}
		if h.Storage == nil && h.BlobSvc != nil {
			if wavBytes, readErr := os.ReadFile(outWavPath); readErr == nil {
				_ = h.BlobSvc.SaveBlob(task.JobID, "wav", wavBytes)
			}
		}

		qualityMap["hwaccel"] = hwReport.ProfileUsed
		qualityMap["fallback_used"] = hwReport.FallbackUsed
		qualityMap["elapsed_ms"] = hwReport.Elapsed.Milliseconds()
		qualityMap["media_duration_seconds"] = durationSec
	} else if payload.TempPath != "" {
		// Legacy conversion path fallback
		dir := filepath.Dir(payload.TempPath)
		aacPath := filepath.Join(dir, task.JobID+".m4a")
		wavPath := filepath.Join(dir, task.JobID+".wav")

		if h.ConvertToAac != nil {
			if err := h.ConvertToAac(payload.TempPath, aacPath); err != nil {
				return domain.StepResult{}, fmt.Errorf("convert to aac failed: %w", err)
			}
			aacBytes, err := os.ReadFile(aacPath)
			if err == nil && h.BlobSvc != nil {
				_ = h.BlobSvc.SaveBlob(task.JobID, "audio_aac", aacBytes)
			}
			_ = os.Remove(aacPath)
		}

		if h.ConvertToWav != nil {
			if err := h.ConvertToWav(payload.TempPath, wavPath); err != nil {
				return domain.StepResult{}, fmt.Errorf("convert to wav failed: %w", err)
			}
			wavBytes, err := os.ReadFile(wavPath)
			if err == nil && h.BlobSvc != nil {
				_ = h.BlobSvc.SaveBlob(task.JobID, "wav", wavBytes)
			}
			_ = os.Remove(wavPath)
		}

		if h.GetMediaDuration != nil {
			durationSec = h.GetMediaDuration(payload.TempPath)
		}
		_ = os.Remove(payload.TempPath)

		qualityMap["media_duration_seconds"] = durationSec
	} else {
		return domain.StepResult{}, fmt.Errorf("no source media available for normalize step")
	}

	qualityJSON, _ := json.Marshal(qualityMap)

	nextStep := domain.StepTranscribe
	nextClass := domain.ResourceClassSTT
	return domain.StepResult{
		NextStep:      &nextStep,
		NextClass:     &nextClass,
		NextPayload:   task.PayloadJSON,
		QualityReport: string(qualityJSON),
	}, nil
}

// TranscribeStepHandler invokes Whisper speech-to-text with parameterized language support.
type TranscribeStepHandler struct {
	BlobSvc       *service.JobBlobService
	Storage       storage.Storage
	WhisperRunner interface {
		RunFromBlobWithOptions(context.Context, string, []byte, *int, intwhisper.RunOptions) (intwhisper.RunResult, error)
	}
	GetJob func(string) *domain.Job
	Logf   func(string, ...any)
	Errf   func(string, error, string, ...any)
}

func (h *TranscribeStepHandler) Execute(ctx context.Context, task *domain.Task) (domain.StepResult, error) {
	var (
		wavBytes []byte
		err      error
	)

	// 1. Load temporary STT WAV from object storage or fallback to blob store
	tempWavKey := fmt.Sprintf("jobs/%s/temp_stt.wav", task.JobID)
	if h.Storage != nil {
		if exists, _ := h.Storage.Exists(ctx, tempWavKey); exists {
			wavBytes, err = h.Storage.Get(ctx, tempWavKey)
		}
	}
	if len(wavBytes) == 0 && h.BlobSvc != nil {
		wavBytes, err = h.BlobSvc.LoadBlob(task.JobID, "wav")
	}
	if len(wavBytes) == 0 {
		return domain.StepResult{}, fmt.Errorf("load wav artifact failed (requires re-entry from normalize step): %w", err)
	}

	var payload struct {
		Language      string `json:"language"`
		RefineEnabled bool   `json:"refine_enabled"`
	}
	_ = json.Unmarshal([]byte(task.PayloadJSON), &payload)

	lang := payload.Language
	if lang == "" {
		lang = "ko"
	}

	var totalSec *int
	if h.GetJob != nil {
		if j := h.GetJob(task.JobID); j != nil && j.MediaDurationSeconds != nil {
			totalSec = j.MediaDurationSeconds
		}
	}

	opts := intwhisper.RunOptions{Language: lang}
	runRes, err := h.WhisperRunner.RunFromBlobWithOptions(ctx, task.JobID, wavBytes, totalSec, opts)
	if err != nil {
		return domain.StepResult{}, fmt.Errorf("whisper transcribe failed: %w", err)
	}

	// Immediate cleanup of temporary uncompressed WAV after successful STT transcription
	if h.Storage != nil {
		_ = h.Storage.Delete(ctx, tempWavKey)
	}
	if h.BlobSvc != nil {
		h.BlobSvc.DeleteBlob(task.JobID, "wav")
	}

	// Save transcript outputs
	if h.BlobSvc != nil {
		_ = h.BlobSvc.SaveBlob(task.JobID, "transcript", []byte(runRes.TimelineText))
		_ = h.BlobSvc.SaveBlob(task.JobID, "transcript_json", runRes.TranscriptJSON)
	}

	qualityJSON, _ := json.Marshal(map[string]any{
		"language": lang,
		"text_len": len(runRes.TimelineText),
	})

	// Check if refine is requested
	refineEnabled := payload.RefineEnabled
	if h.GetJob != nil {
		if j := h.GetJob(task.JobID); j != nil {
			refineEnabled = j.RefineEnabled
		}
	}

	if refineEnabled {
		nextStep := domain.StepPolish
		nextClass := domain.ResourceClassLLM
		return domain.StepResult{
			NextStep:      &nextStep,
			NextClass:     &nextClass,
			NextPayload:   task.PayloadJSON,
			QualityReport: string(qualityJSON),
		}, nil
	}

	// If no refine requested, entire audio transcription is complete
	return domain.StepResult{
		QualityReport: string(qualityJSON),
	}, nil
}

// PolishStepHandler executes Stage 1 LLM Timeline refinement using configured model/thinking.
type PolishStepHandler struct {
	BlobSvc                  *service.JobBlobService
	PolishTranscriptTimeline func(ctx context.Context, jobID, transcript string, cfg gemini.LLMStageConfig) (string, error)
	GetUserSettings          func(ctx context.Context, userID string) (*domain.UserSettings, error)
	Logf                     func(string, ...any)
	Errf                     func(string, error, string, ...any)
}

func (h *PolishStepHandler) Execute(ctx context.Context, task *domain.Task) (domain.StepResult, error) {
	if h.BlobSvc == nil {
		return domain.StepResult{}, fmt.Errorf("blob service unavailable")
	}

	rawBytes, err := h.BlobSvc.LoadBlob(task.JobID, "transcript")
	if err != nil {
		return domain.StepResult{}, fmt.Errorf("load transcript blob failed: %w", err)
	}

	// Resolve stage config
	var userPrefs *gemini.UserLLMPreferences
	if h.GetUserSettings != nil {
		if s, err := h.GetUserSettings(ctx, task.OwnerID); err == nil && s != nil {
			var p gemini.UserLLMPreferences
			if json.Unmarshal([]byte(s.LLMPrefsJSON), &p) == nil {
				userPrefs = &p
			}
		}
	}

	stageCfg := gemini.ResolveStageConfig("polish", nil, userPrefs, gemini.DefaultPreferences().Polish)

	polishedTimeline, err := h.PolishTranscriptTimeline(ctx, task.JobID, string(rawBytes), stageCfg)
	if err != nil {
		return domain.StepResult{}, fmt.Errorf("polish transcript failed: %w", err)
	}

	_ = h.BlobSvc.SaveBlob(task.JobID, "refined_timeline", []byte(polishedTimeline))

	paramsJSON, _ := json.Marshal(stageCfg)
	nextStep := domain.StepStructure
	nextClass := domain.ResourceClassLLM
	return domain.StepResult{
		NextStep:    &nextStep,
		NextClass:   &nextClass,
		NextPayload: task.PayloadJSON,
		ParamsJSON:  string(paramsJSON),
	}, nil
}

// StructureStepHandler executes Stage 2 LLM paragraph structuring.
type StructureStepHandler struct {
	BlobSvc                       *service.JobBlobService
	StructureTranscriptParagraphs func(ctx context.Context, jobID, timeline string, cfg gemini.LLMStageConfig) (string, error)
	GetUserSettings               func(ctx context.Context, userID string) (*domain.UserSettings, error)
	Logf                          func(string, ...any)
	Errf                          func(string, error, string, ...any)
}

func (h *StructureStepHandler) Execute(ctx context.Context, task *domain.Task) (domain.StepResult, error) {
	if h.BlobSvc == nil {
		return domain.StepResult{}, fmt.Errorf("blob service unavailable")
	}

	timelineBytes, err := h.BlobSvc.LoadBlob(task.JobID, "refined_timeline")
	if err != nil {
		return domain.StepResult{}, fmt.Errorf("load refined_timeline blob failed: %w", err)
	}

	var userPrefs *gemini.UserLLMPreferences
	if h.GetUserSettings != nil {
		if s, err := h.GetUserSettings(ctx, task.OwnerID); err == nil && s != nil {
			var p gemini.UserLLMPreferences
			if json.Unmarshal([]byte(s.LLMPrefsJSON), &p) == nil {
				userPrefs = &p
			}
		}
	}

	stageCfg := gemini.ResolveStageConfig("structure", nil, userPrefs, gemini.DefaultPreferences().Structure)

	structuredJSON, err := h.StructureTranscriptParagraphs(ctx, task.JobID, string(timelineBytes), stageCfg)
	if err != nil {
		return domain.StepResult{}, fmt.Errorf("structure paragraphs failed: %w", err)
	}

	_ = h.BlobSvc.SaveBlob(task.JobID, "refined", []byte(structuredJSON))

	paramsJSON, _ := json.Marshal(stageCfg)
	return domain.StepResult{
		ParamsJSON: string(paramsJSON),
	}, nil
}

// PDFRenderStepHandler verifies PDF integrity, page count, and prepares chunking state.
type PDFRenderStepHandler struct {
	BlobSvc            *service.JobBlobService
	CountPDFPages      func(string) (int, error)
	PDFMaxPages        int
	MaxPagesPerRequest int
	Logf               func(string, ...any)
	Errf               func(string, error, string, ...any)
}

func (h *PDFRenderStepHandler) Execute(ctx context.Context, task *domain.Task) (domain.StepResult, error) {
	var payload struct {
		TempPath string `json:"temp_path"`
	}
	_ = json.Unmarshal([]byte(task.PayloadJSON), &payload)

	if payload.TempPath == "" {
		return domain.StepResult{}, fmt.Errorf("pdf render missing temp_path")
	}

	countPages := h.CountPDFPages
	if countPages == nil {
		return domain.StepResult{}, fmt.Errorf("pdf counter unavailable")
	}

	pageCount, err := countPages(payload.TempPath)
	if err != nil {
		return domain.StepResult{}, fmt.Errorf("count pdf pages failed: %w", err)
	}

	maxPages := h.PDFMaxPages
	if maxPages <= 0 {
		maxPages = 500
	}
	if pageCount > maxPages {
		return domain.StepResult{}, fmt.Errorf("pdf page limit exceeded: %d > %d", pageCount, maxPages)
	}

	maxPerReq := h.MaxPagesPerRequest
	if maxPerReq <= 0 {
		maxPerReq = 10
	}

	totalChunks := (pageCount + maxPerReq - 1) / maxPerReq
	chunkEnd := maxPerReq
	if chunkEnd > pageCount {
		chunkEnd = pageCount
	}

	// Save original pdf to blob storage if blob service available
	if h.BlobSvc != nil {
		if data, err := os.ReadFile(payload.TempPath); err == nil {
			_ = h.BlobSvc.SaveBlob(task.JobID, "pdf_original", data)
		}
	}

	nextPayload, _ := json.Marshal(map[string]any{
		"temp_path":     payload.TempPath,
		"page_count":    pageCount,
		"total_chunks":  totalChunks,
		"current_chunk": 1,
		"chunk_start":   1,
		"chunk_end":     chunkEnd,
	})

	nextStep := domain.StepPDFChunk
	nextClass := domain.ResourceClassLLM
	return domain.StepResult{
		NextStep:      &nextStep,
		NextClass:     &nextClass,
		NextPayload:   string(nextPayload),
		QualityReport: fmt.Sprintf(`{"pages":%d,"total_chunks":%d}`, pageCount, totalChunks),
	}, nil
}

// PDFChunkStepHandler performs on-demand rendering of a page range and sends it to Gemini.
type PDFChunkStepHandler struct {
	BlobSvc               *service.JobBlobService
	RenderPDFToJPEGsRange func(pdfPath string, startPage, endPage int, outDir string) ([]string, error)
	ExtractDocumentChunk  func(ctx context.Context, chunk domain.DocumentChunk, consistencyContext string, cfg gemini.LLMStageConfig) ([]byte, error)
	GetUserSettings       func(ctx context.Context, userID string) (*domain.UserSettings, error)
	MaxPagesPerRequest    int
	Logf                  func(string, ...any)
	Errf                  func(string, error, string, ...any)
}

func (h *PDFChunkStepHandler) Execute(ctx context.Context, task *domain.Task) (domain.StepResult, error) {
	var payload struct {
		TempPath     string `json:"temp_path"`
		PageCount    int    `json:"page_count"`
		TotalChunks  int    `json:"total_chunks"`
		CurrentChunk int    `json:"current_chunk"`
		ChunkStart   int    `json:"chunk_start"`
		ChunkEnd     int    `json:"chunk_end"`
	}
	_ = json.Unmarshal([]byte(task.PayloadJSON), &payload)

	if payload.TempPath == "" || payload.PageCount <= 0 {
		return domain.StepResult{}, fmt.Errorf("invalid pdf chunk payload")
	}

	tmpDir, err := os.MkdirTemp("", "pdf_chunk_*")
	if err != nil {
		return domain.StepResult{}, fmt.Errorf("create temp dir for chunk failed: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if h.RenderPDFToJPEGsRange == nil {
		return domain.StepResult{}, fmt.Errorf("render pdf range unavailable")
	}

	imagePaths, err := h.RenderPDFToJPEGsRange(payload.TempPath, payload.ChunkStart, payload.ChunkEnd, tmpDir)
	if err != nil {
		return domain.StepResult{}, fmt.Errorf("render pdf range failed: %w", err)
	}
	if len(imagePaths) == 0 {
		return domain.StepResult{}, fmt.Errorf("no rendered pages for range %d-%d", payload.ChunkStart, payload.ChunkEnd)
	}

	images := make([]domain.DocumentPageImage, 0, len(imagePaths))
	for offset, p := range imagePaths {
		data, err := os.ReadFile(p)
		if err != nil {
			return domain.StepResult{}, fmt.Errorf("read rendered page failed: %w", err)
		}
		images = append(images, domain.DocumentPageImage{
			PageIndex: payload.ChunkStart + offset,
			MIMEType:  "image/jpeg",
			Data:      data,
		})
	}

	// Resolve stage config for LLM
	var userPrefs *gemini.UserLLMPreferences
	if h.GetUserSettings != nil {
		if s, err := h.GetUserSettings(ctx, task.OwnerID); err == nil && s != nil {
			var p gemini.UserLLMPreferences
			if json.Unmarshal([]byte(s.LLMPrefsJSON), &p) == nil {
				userPrefs = &p
			}
		}
	}
	stageCfg := gemini.ResolveStageConfig("pdf_extract", nil, userPrefs, gemini.DefaultPreferences().PDFExtract)

	if h.ExtractDocumentChunk == nil {
		return domain.StepResult{}, fmt.Errorf("gemini document extraction unavailable")
	}

	chunkModel := domain.DocumentChunk{
		ChunkIndex:  payload.CurrentChunk,
		TotalChunks: payload.TotalChunks,
		StartPage:   payload.ChunkStart,
		EndPage:     payload.ChunkEnd,
		TotalPages:  payload.PageCount,
		Images:      images,
	}

	resultJSON, err := h.ExtractDocumentChunk(ctx, chunkModel, "", stageCfg)
	if err != nil {
		return domain.StepResult{}, fmt.Errorf("extract document chunk failed: %w", err)
	}

	// Save chunk result blob
	if h.BlobSvc != nil {
		blobKind := fmt.Sprintf("pdf_chunk_json_%d", payload.CurrentChunk)
		if err := h.BlobSvc.SaveBlob(task.JobID, blobKind, resultJSON); err != nil {
			return domain.StepResult{}, fmt.Errorf("save chunk blob failed: %w", err)
		}
	}

	maxPerReq := h.MaxPagesPerRequest
	if maxPerReq <= 0 {
		maxPerReq = 10
	}

	if payload.CurrentChunk < payload.TotalChunks {
		nextChunk := payload.CurrentChunk + 1
		nextStart := payload.ChunkEnd + 1
		nextEnd := nextStart + maxPerReq - 1
		if nextEnd > payload.PageCount {
			nextEnd = payload.PageCount
		}

		nextPayload, _ := json.Marshal(map[string]any{
			"temp_path":     payload.TempPath,
			"page_count":    payload.PageCount,
			"total_chunks":  payload.TotalChunks,
			"current_chunk": nextChunk,
			"chunk_start":   nextStart,
			"chunk_end":     nextEnd,
		})
		nextStep := domain.StepPDFChunk
		nextClass := domain.ResourceClassLLM
		return domain.StepResult{
			NextStep:    &nextStep,
			NextClass:   &nextClass,
			NextPayload: string(nextPayload),
		}, nil
	}

	// All chunks processed -> Transition to PDF merge step
	nextPayload, _ := json.Marshal(map[string]any{
		"total_chunks": payload.TotalChunks,
		"page_count":   payload.PageCount,
	})
	nextStep := domain.StepPDFMerge
	nextClass := domain.ResourceClassMedia
	return domain.StepResult{
		NextStep:    &nextStep,
		NextClass:   &nextClass,
		NextPayload: string(nextPayload),
	}, nil
}

// PDFMergeStepHandler merges all individual chunk JSONs into the final document JSON.
type PDFMergeStepHandler struct {
	BlobSvc           *service.JobBlobService
	MergeDocumentJSON func(...[]byte) ([]byte, error)
	Logf              func(string, ...any)
	Errf              func(string, error, string, ...any)
}

func (h *PDFMergeStepHandler) Execute(ctx context.Context, task *domain.Task) (domain.StepResult, error) {
	if h.BlobSvc == nil {
		return domain.StepResult{}, fmt.Errorf("blob service unavailable")
	}

	var payload struct {
		TotalChunks int `json:"total_chunks"`
		PageCount   int `json:"page_count"`
	}
	_ = json.Unmarshal([]byte(task.PayloadJSON), &payload)

	if payload.TotalChunks <= 0 {
		return domain.StepResult{}, fmt.Errorf("invalid total_chunks in pdf merge payload")
	}

	chunkResults := make([][]byte, 0, payload.TotalChunks)
	for i := 1; i <= payload.TotalChunks; i++ {
		b, err := h.BlobSvc.LoadBlob(task.JobID, fmt.Sprintf("pdf_chunk_json_%d", i))
		if err != nil {
			return domain.StepResult{}, fmt.Errorf("load pdf chunk %d failed: %w", i, err)
		}
		chunkResults = append(chunkResults, b)
	}

	mergeFn := h.MergeDocumentJSON
	if mergeFn == nil {
		return domain.StepResult{}, fmt.Errorf("document merge function unavailable")
	}

	mergedJSON, err := mergeFn(chunkResults...)
	if err != nil {
		return domain.StepResult{}, fmt.Errorf("merge document json failed: %w", err)
	}

	if err := h.BlobSvc.SaveDocumentJSON(task.JobID, mergedJSON); err != nil {
		return domain.StepResult{}, fmt.Errorf("save final document json failed: %w", err)
	}

	// Cleanup chunk blobs
	for i := 1; i <= payload.TotalChunks; i++ {
		h.BlobSvc.DeleteBlob(task.JobID, fmt.Sprintf("pdf_chunk_json_%d", i))
	}

	return domain.StepResult{
		QualityReport: fmt.Sprintf(`{"merged_chunks":%d,"pages":%d}`, payload.TotalChunks, payload.PageCount),
	}, nil
}
