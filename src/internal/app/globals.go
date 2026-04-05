package app

import (
	"os"
	"path/filepath"
	"regexp"

	httpx "whisperserver/src/internal/http"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	statusPending         = "작업 대기 중"
	statusRunning         = "작업 중"
	statusRefiningPending = "정제 대기 중"
	statusRefining        = "정제 중"
	statusCompleted       = "완료"
	statusFailed          = "실패"
)

var (
	projectRoot, _ = os.Getwd()
	tmpFolder      = filepath.Join(projectRoot, ".run", "tmp")
	templateDir    = filepath.Join(projectRoot, "templates")
	staticDir      = filepath.Join(projectRoot, "static")
	spaIndexPath   = filepath.Join(staticDir, "app", "index.html")
	modelDir       = filepath.Join(projectRoot, "whisper", "models")
	whisperCLI     = filepath.Join(projectRoot, "whisper", "bin", "whisper-cli")

	allowedExtensions             = map[string]struct{}{"mp3": {}, "wav": {}, "m4a": {}, "pdf": {}}
	chunkSize                     = 4 * 1024 * 1024
	maxUploadSizeMB               int
	uploadRateLimitKB             int
	jobTimeoutSec                 int
	geminiModel                   string
	splitTaskQueues               bool
	pdfMaxPages                   int
	pdfMaxPagesPerRequest         int
	pdfRenderDPI                  int
	pdfBatchTimeoutSec            int
	pdfMaxRenderedImageBytes      int64
	pdfConsistencyContextMaxChars int
	pdfToolPDFInfo                string
	pdfToolPDFToPPM               string

	secureRe   = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
	lineRe1    = regexp.MustCompile(`\[(\d{2}):(\d{2}):(\d{2}\.\d+)`)
	lineRe2    = regexp.MustCompile(`\[(\d{2}):(\d{2}):(\d{2})`)
	progressRe = regexp.MustCompile(`\[(\d{2}):(\d{2}):(\d{2}(?:\.\d+)?)\s*-->`)

	jobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "whisper_jobs_total", Help: "Total jobs finished by status"},
		[]string{"status"},
	)
	jobsInProgress = prometheus.NewGauge(prometheus.GaugeOpts{Name: "whisper_jobs_in_progress", Help: "Jobs currently being processed"})
	jobDurationSec = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "whisper_job_duration_seconds", Help: "Duration of jobs in seconds"})
	uploadBytes    = prometheus.NewCounter(prometheus.CounterOpts{Name: "whisper_upload_bytes_total", Help: "Total bytes uploaded"})
	queueLength    = prometheus.NewGauge(prometheus.GaugeOpts{Name: "whisper_task_queue_size", Help: "Task queue size"})
)

const refineSystemPrompt = `# Role
You are a professional 'Speech-to-Text (STT) Correction Editor.' You possess an exceptional ability to grasp the context of fragmented transcription data and refine incomplete sentences into natural, accurate spoken scripts-not formal written text. Your highest priority is to preserve every detail of the original speech without omitting any content.

# Task
The provided [Original] text is a result of transcribing lectures or speeches using an STT engine. It contains typos, spacing errors, grammatical mistakes, and fragmented sentences. Refine this text according to the [Guidelines] below, ensuring **Zero Omission**.

# Guidelines
1. **Contextual Correction:**
   - **Correct mis-transcribed words** that sound similar based on the context. (e.g., '정보의미' -> '정보 은닉', '이네이턴스' -> 'Inheritance(상속)')
   - Ensure technical terms use accurate notation. Format code variables or operators according to programming syntax. (e.g., '데이터 스트럭처' -> 'Data Structure(자료구조)', 'M 퍼센트' -> '&')

2. **No Omission:**
   - **Never summarize the content or shorten sentences.** Be vigilant against the tendency to merge or condense sentences toward the end of the text.
   - Do not arbitrarily delete any part of the original speech, including the speaker's intent, small talk, additional explanations, or exclamations.
   - Every spoken element must be included in the output. (Meaningless repetitive stammers or filler sounds may be cleaned up naturally.)
   - Do not change the original meaning or distort facts during the refining process.
   - The volume of the output text must be nearly identical to the volume of the original text.

3. **Complete Sentence Construction:**
   - Transform lists of fragmented words into grammatically correct sentences. Use commas (,) and periods (.) appropriately to enhance readability.

4. **Contextual Paragraphing:**
   - Group sentences that discuss a single topic into a paragraph.
   - This means creating a paragraph that contains the refined sentences, not merging them into one long sentence. All sentences within a paragraph must be output as refined.
   - Start a new paragraph when the topic shifts or the flow of conversation changes.

5. **Timeline Integrity:**
   - Never arbitrarily modify or omit the timestamps (start_time) assigned to each sentence in the [Original] data.
   - Maintain precise timeline mapping for each refined sentence, even when grouping them into paragraphs.

# Output Format
{ "paragraph": [ { "paragraph_summary": "문단 요약 정리", "sentence": [ { "start_time": "[00:00:00,000]", "content": "문장 정제 내용1" } ] } ] }`

type JobView = httpx.JobView
type JobRow = httpx.JobRow
type FolderRow = httpx.FolderRow
