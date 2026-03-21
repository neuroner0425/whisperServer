package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"whisperserver/src/internal/model"
	"whisperserver/src/internal/store"
	intutil "whisperserver/src/internal/util"
)

func (w *Worker) processTask(t task, splitMode bool) {
	job := w.deps.GetJob(t.jobID)
	if job == nil || job.IsTrashed {
		return
	}

	switch t.kind {
	case taskTypeTranscribe:
		if job.Status != w.cfg.StatusPending && job.Status != w.cfg.StatusRunning {
			return
		}
		if err := w.taskTranscribe(t.jobID); err != nil {
			w.deps.Errf("worker.transcribe", err, "job_id=%s", t.jobID)
			return
		}
		updated := w.deps.GetJob(t.jobID)
		if updated == nil || updated.Status != w.cfg.StatusRefiningPending {
			return
		}
		if splitMode {
			w.EnqueueRefine(t.jobID)
			w.deps.Logf("[WORKER] queued refine job_id=%s", t.jobID)
			return
		}
		w.finalizeRefine(t.jobID)
	case taskTypeRefine:
		if job.Status != w.cfg.StatusRefiningPending && job.Status != w.cfg.StatusRefining {
			return
		}
		w.finalizeRefine(t.jobID)
	}
}

func (w *Worker) finalizeRefine(jobID string) {
	job := w.deps.GetJob(jobID)
	if job == nil || job.IsTrashed {
		return
	}
	b, err := store.LoadJobBlob(jobID, store.BlobKindTranscript)
	if err != nil {
		w.deps.Errf("worker.loadTranscriptBlob", err, "job_id=%s", jobID)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		return
	}
	if err := w.taskRefining(jobID, string(b)); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.Errf("worker.refine", err, "job_id=%s", jobID)
		return
	}
	if updated := w.deps.GetJob(jobID); updated == nil || updated.IsTrashed {
		return
	}
	w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusCompleted, "result": "db://transcript"})
	w.deps.Logf("[WORKER] completed job_id=%s result=db://transcript", jobID)
}

func (w *Worker) taskTranscribe(jobID string) error {
	w.deps.Logf("[TRANSCRIBE] start job_id=%s input=db://wav", jobID)
	started := time.Now()
	w.deps.SetJobFields(jobID, map[string]any{
		"status":       w.cfg.StatusRunning,
		"started_at":   started.Format("2006-01-02 15:04:05"),
		"started_ts":   float64(started.Unix()),
		"preview_text": "",
	})
	store.DeleteJobBlob(jobID, store.BlobKindPreview)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(w.cfg.JobTimeoutSec)*time.Second)
	w.setCancel(jobID, cancel)
	defer func() {
		cancel()
		w.setCancel(jobID, nil)
	}()

	job := w.deps.GetJob(jobID)
	totalSec := job.MediaDurationSeconds
	wavBytes, err := os.ReadFile(filepath.Join(w.cfg.TmpFolder, jobID+".wav"))
	if err != nil {
		w.deps.Errf("transcribe.loadWavFile", err, "job_id=%s", jobID)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.IncJobsTotal("failure")
		return err
	}
	timelineText, err := w.runWhisperFromBlob(ctx, jobID, wavBytes, totalSec)
	if err != nil {
		statusLabel := "failure"
		fields := map[string]any{"status": w.cfg.StatusFailed}
		if errors.Is(err, context.DeadlineExceeded) {
			fields["status_detail"] = "타임아웃"
			statusLabel = "timeout"
		}
		w.deps.SetJobFields(jobID, fields)
		w.deps.IncJobsTotal(statusLabel)
		w.deps.Errf("transcribe.runWhisper", err, "job_id=%s", jobID)
		_ = os.Remove(filepath.Join(w.cfg.TmpFolder, jobID+".wav"))
		return err
	}
	if updated := w.deps.GetJob(jobID); updated == nil || updated.IsTrashed {
		return nil
	}

	if err := store.SaveJobBlob(jobID, store.BlobKindTranscript, []byte(timelineText)); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.IncJobsTotal("failure")
		w.deps.Errf("transcribe.saveTranscriptBlob", err, "job_id=%s", jobID)
		return err
	}

	completed := time.Now()
	w.deps.IncJobsTotal("success")
	w.deps.ObserveJobDuration(completed.Sub(started).Seconds())

	nextStatus := w.cfg.StatusCompleted
	if job.RefineEnabled && w.deps.HasGeminiConfigured() {
		nextStatus = w.cfg.StatusRefiningPending
	}

	w.deps.SetJobFields(jobID, map[string]any{
		"status":       nextStatus,
		"result":       "db://transcript",
		"completed_at": completed.Format("2006-01-02 15:04:05"),
		"completed_ts": float64(completed.Unix()),
		"duration":     intutil.FormatSeconds(int(completed.Sub(started).Seconds())),
	})
	_ = os.Remove(filepath.Join(w.cfg.TmpFolder, jobID+".wav"))
	w.deps.Logf("[TRANSCRIBE] cleaned input file job_id=%s", jobID)
	w.deps.Logf("[TRANSCRIBE] done job_id=%s output=db://transcript status=%s duration_sec=%d", jobID, nextStatus, int(completed.Sub(started).Seconds()))
	return nil
}

func (w *Worker) taskRefining(jobID, timelineText string) error {
	w.deps.Logf("[REFINE] start job_id=%s", jobID)
	if updated := w.deps.GetJob(jobID); updated == nil || updated.IsTrashed {
		return nil
	}
	w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusRefining})
	if !w.deps.HasGeminiConfigured() {
		w.deps.Logf("[REFINE] skipped job_id=%s reason=no gemini key", jobID)
		return nil
	}
	job := w.deps.GetJob(jobID)
	desc := w.buildRefineDescription(job)
	refined, err := w.deps.RefineTranscript(timelineText, desc)
	if err != nil || strings.TrimSpace(refined) == "" {
		if err != nil {
			w.deps.Errf("refine.refineTranscript", err, "job_id=%s", jobID)
		} else {
			w.deps.Logf("[REFINE] empty result job_id=%s", jobID)
		}
		return err
	}
	if err := store.SaveJobBlob(jobID, store.BlobKindRefined, []byte(refined)); err != nil {
		w.deps.Errf("refine.saveRefinedBlob", err, "job_id=%s", jobID)
		return err
	}
	if updated := w.deps.GetJob(jobID); updated == nil || updated.IsTrashed {
		return nil
	}
	w.deps.SetJobFields(jobID, map[string]any{"result_refined": "db://refined"})
	w.deps.Logf("[REFINE] done job_id=%s output=db://refined", jobID)
	return nil
}

func (w *Worker) buildRefineDescription(job *model.Job) string {
	base := strings.TrimSpace(job.Description)
	ownerID := strings.TrimSpace(job.OwnerID)
	tags := w.deps.UniqueStrings(job.Tags)
	if ownerID == "" || len(tags) == 0 {
		return base
	}

	descMap, err := w.deps.GetTagDescriptions(ownerID, tags)
	if err != nil {
		w.deps.Errf("refine.getTagDescriptions", err, "owner_id=%s", ownerID)
		return base
	}

	tagLines := make([]string, 0, len(tags))
	for _, t := range tags {
		d := strings.TrimSpace(descMap[t])
		if d == "" {
			continue
		}
		tagLines = append(tagLines, fmt.Sprintf("[%s] %s", t, d))
	}
	if len(tagLines) == 0 {
		return base
	}
	if base == "" {
		return strings.Join(tagLines, "\n")
	}
	return base + "\n\n" + strings.Join(tagLines, "\n")
}
