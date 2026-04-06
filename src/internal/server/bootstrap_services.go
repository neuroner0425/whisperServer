package server

import (
	"errors"
	"mime/multipart"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	intgemini "whisperserver/src/internal/integrations/gemini"
	intwhisper "whisperserver/src/internal/integrations/whisper"
	store "whisperserver/src/internal/repo/sqlite"
	intruntime "whisperserver/src/internal/runtime"
	"whisperserver/src/internal/service"
	intutil "whisperserver/src/internal/util"
	"whisperserver/src/internal/worker"
)

type appServices struct {
	blobSvc    *service.JobBlobService
	folderSvc  *service.FolderService
	lifecycle  *service.JobLifecycle
	runtime    *intruntime.Runtime
	tagSvc     *service.TagService
	storageSvc *service.StorageService
	uploadSvc  *service.UploadService
	geminiRt   *intgemini.Runtime
	whisperRt  *intwhisper.Runtime
}

func newAppServices() appServices {
	runtime := appRuntime
	blobSvc := service.NewJobBlobService(service.JobBlobServiceDeps{
		HasJobBlob:                 store.HasJobBlob,
		LoadJobBlob:                store.LoadJobBlob,
		SaveJobBlob:                store.SaveJobBlob,
		DeleteJobBlob:              store.DeleteJobBlob,
		ListJobBlobKinds:           store.ListJobBlobKinds,
		BlobKindAudioAAC:           store.BlobKindAudioAAC,
		BlobKindPreview:            store.BlobKindPreview,
		BlobKindPDFOriginal:        store.BlobKindPDFOriginal,
		BlobKindDocumentJSON:       store.BlobKindDocumentJSON,
		BlobKindDocumentMarkdown:   store.BlobKindDocumentMarkdown,
		BlobKindDocumentChunkIndex: store.BlobKindDocumentChunkIndex,
		BlobKindTranscript:         store.BlobKindTranscript,
		BlobKindTranscriptJSON:     store.BlobKindTranscriptJSON,
		BlobKindRefined:            store.BlobKindRefined,
	})
	folderSvc := service.NewFolderService(service.FolderServiceDeps{
		GetFolderByID:               store.GetFolderByID,
		CreateFolder:                store.CreateFolder,
		RenameFolder:                store.RenameFolder,
		SetFolderTrashed:            store.SetFolderTrashed,
		MoveFolder:                  store.MoveFolder,
		TouchFolderAndAncestors:     store.TouchFolderAndAncestors,
		IsFolderDescendant:          store.IsFolderDescendant,
		ListAllFoldersByOwner:       store.ListAllFoldersByOwner,
		ListFolderPath:              store.ListFolderPath,
		DeleteTrashedFoldersByOwner: store.DeleteTrashedFoldersByOwner,
	})
	tagSvc := service.NewTagService(service.TagServiceDeps{
		ListTagsByOwner:     store.ListTagsByOwner,
		UpsertTag:           store.UpsertTag,
		DeleteTag:           store.DeleteTag,
		ListTagNamesByOwner: store.ListTagNamesByOwner,
	})
	storageSvc := service.NewStorageService(service.StorageServiceDeps{
		ListJobBlobUsageByOwner: func(ownerID string) ([]service.JobBlobUsage, error) {
			usages, err := store.ListJobBlobUsageByOwner(ownerID)
			if err != nil {
				return nil, err
			}
			out := make([]service.JobBlobUsage, 0, len(usages))
			for _, u := range usages {
				out = append(out, service.JobBlobUsage{JobID: u.JobID, Bytes: u.Bytes, BlobCount: u.BlobCount})
			}
			return out, nil
		},
	})
	var notify func(string, string, map[string]any)
	if runtime != nil && runtime.Broker() != nil {
		notify = runtime.Broker().Notify
	}
	lifecycle := service.NewJobLifecycle(service.JobLifecycleDeps{
		CancelJob:        runtime.CancelJob,
		RemoveTempWav:    runtime.RemoveTempWav,
		SetJobFields:     runtime.SetJobFields,
		DeleteJobBlob:    store.DeleteJobBlob,
		ListJobBlobKinds: store.ListJobBlobKinds,
		Notify:           notify,
		StatusPending:    statusPending,
	})
	uploadSvc := service.NewUploadService(service.UploadServiceDeps{
		DetectFileType:          func(name string) string { return intutil.DetectFileType(name) },
		AllowedFile:             func(name string) bool { return intutil.AllowedFile(name, allowedExtensions) },
		SortedExts:              func() []string { return intutil.SortedExts(allowedExtensions) },
		ListTagNamesByOwner:     store.ListTagNamesByOwner,
		GetFolderByID:           store.GetFolderByID,
		TouchFolderAndAncestors: store.TouchFolderAndAncestors,
		SaveUploadWithLimit: func(h *multipart.FileHeader, dst string, maxBytes int64, bytesPerSec int64) (int64, error) {
			return intutil.SaveUploadWithLimit(h, dst, maxBytes, chunkSize, bytesPerSec)
		},
		IsUploadTooLarge:    func(err error) bool { return errors.Is(err, intutil.ErrUploadTooLarge) },
		ConvertToAac:        intutil.ConvertToAac,
		GetMediaDuration:    intutil.GetMediaDuration,
		FormatSecondsPtr:    intutil.FormatSecondsPtr,
		SaveJobBlob:         store.SaveJobBlob,
		BlobKindAudioAAC:    store.BlobKindAudioAAC,
		BlobKindPDFOriginal: store.BlobKindPDFOriginal,
		AddJob:              runtime.AddJob,
		SetJobFields:        runtime.SetJobFields,
		EnqueueTranscribe:   runtime.EnqueueTranscribe,
		EnqueuePDFExtract:   runtime.EnqueuePDFExtract,
		UploadBytesAdd:      uploadBytes.Add,
		Logf:                procLogf,
		Errf:                procErrf,
		TmpFolder:           tmpFolder,
		MaxUploadSizeMB:     maxUploadSizeMB,
		UploadRateLimitKBPS: uploadRateLimitKB,
		StatusPending:       statusPending,
		StatusFailed:        statusFailed,
		Now:                 time.Now,
		NewJobID:            uuid.NewString,
		Spawn:               func(fn func()) { go fn() },
	})
	geminiRt := intgemini.New(intgemini.Config{
		Model:                         geminiModel,
		APIKeys:                       geminiAPIKeysFromConfig(),
		PDFBatchTimeoutSec:            pdfBatchTimeoutSec,
		PDFConsistencyContextMaxChars: pdfConsistencyContextMaxChars,
		PromptPath:                    filepath.Join(projectRoot, "docs", "prompts", "file_transcript_system_prompt.md"),
		Logf:                          procLogf,
		Errf:                          procErrf,
	})
	whisperRt := intwhisper.New(intwhisper.Config{
		ModelDir:      modelDir,
		WhisperCLI:    whisperCLI,
		JobTimeoutSec: jobTimeoutSec,
		ProgressRe:    progressRe,
		Logf:          procLogf,
		Errf:          procErrf,
		OnPhase: func(jobID, phase string, percent int, label string) {
			runtime.SetJobFields(jobID, map[string]any{"phase": phase, "progress_percent": percent, "progress_label": label})
		},
		OnPreviewLine: runtime.AppendJobPreviewLine,
		OnPreviewText: runtime.ReplaceJobPreviewText,
	})
	return appServices{blobSvc: blobSvc, folderSvc: folderSvc, lifecycle: lifecycle, runtime: runtime, tagSvc: tagSvc, storageSvc: storageSvc, uploadSvc: uploadSvc, geminiRt: geminiRt, whisperRt: whisperRt}
}

func newAppWorker(blobSvc *service.JobBlobService, geminiRt *intgemini.Runtime, whisperRt *intwhisper.Runtime) *worker.Worker {
	runtime := appRuntime
	return worker.New(worker.Config{
		SplitTaskQueues:          splitTaskQueues,
		TmpFolder:                tmpFolder,
		ModelDir:                 modelDir,
		WhisperCLI:               whisperCLI,
		JobTimeoutSec:            jobTimeoutSec,
		PDFBatchTimeoutSec:       pdfBatchTimeoutSec,
		PDFMaxPages:              pdfMaxPages,
		PDFMaxPagesPerRequest:    pdfMaxPagesPerRequest,
		PDFMaxRenderedImageBytes: pdfMaxRenderedImageBytes,
		ProgressRe:               progressRe,
		StatusPending:            statusPending,
		StatusRunning:            statusRunning,
		StatusRefiningPending:    statusRefiningPending,
		StatusRefining:           statusRefining,
		StatusCompleted:          statusCompleted,
		StatusFailed:             statusFailed,
	}, worker.Deps{
		GetJob:                runtime.GetJob,
		SetJobFields:          runtime.SetJobFields,
		AppendJobPreviewLine:  runtime.AppendJobPreviewLine,
		ReplaceJobPreviewText: runtime.ReplaceJobPreviewText,
		BlobSvc:               blobSvc,
		ConvertToWav:          intutil.ConvertToWav,
		WhisperRunner:         whisperRt,
		HasGeminiConfigured:   geminiRt.HasConfigured,
		RefineTranscript:      geminiRt.RefineTranscript,
		CountPDFPages:         func(path string) (int, error) { return intutil.CountPDFPages(pdfToolPDFInfo, path) },
		RenderPDFToJPEGs: func(pdfPath, outDir string) ([]string, error) {
			return intutil.RenderPDFToJPEGs(pdfToolPDFToPPM, pdfPath, outDir, pdfRenderDPI)
		},
		ExtractDocumentChunk:    geminiRt.ExtractDocumentChunk,
		BuildConsistencyContext: geminiRt.BuildConsistencyContext,
		MergeDocumentJSON:       geminiRt.MergeDocumentJSON,
		RenderDocumentMarkdown:  geminiRt.RenderDocumentMarkdown,
		UniqueStrings:           intutil.UniqueStringsKeepOrder,
		GetTagDescriptions:      store.GetTagDescriptionsByNames,
		Logf:                    procLogf,
		Errf:                    procErrf,
		IncInProgress:           jobsInProgress.Inc,
		DecInProgress:           jobsInProgress.Dec,
		SetQueueLength:          queueLength.Set,
		IncJobsTotal:            func(status string) { jobsTotal.WithLabelValues(status).Inc() },
		ObserveJobDuration:      jobDurationSec.Observe,
	})
}
