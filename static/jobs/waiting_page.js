(function () {
    const statusEl = document.querySelector('.status');
    const progressLabel = document.querySelector('[data-role="progress-label"]');
    const progressBar = document.querySelector('[data-role="progress-bar"]');
    const livePreviewEl = document.getElementById('live-preview');
    const toggleBtn = document.getElementById('detail-toggle-btn');
    const detailPanel = document.getElementById('detail-panel');
    const openModalBtn = document.getElementById('open-tag-modal-btn');
    const closeModalBtn = document.getElementById('close-tag-modal-btn');
    const modal = document.getElementById('tag-modal');
    const pageDataEl = document.getElementById('waiting-page-data');
    if (!pageDataEl) return;

    const pageData = JSON.parse(pageDataEl.textContent || '{}');
    const initialRawPreview = pageData.preview_text || '';
    const jobId = pageData.job_id || '';

    let isPolling = true;
    let shownProgress = Number(pageData.progress_percent || 0);
    let shownPreview = '';
    let currentPhase = pageData.phase || '대기 중';
    let pendingRedirect = false;
    let playbackRunning = false;
    const snapshots = [];
    const OVERDUE_SPEEDUP = 2.5;
    const NORMAL_MIN_DURATION_MS = 600;
    const FAST_MIN_DURATION_MS = 240;
    const OVERDUE_THRESHOLD_MS = 400;
    let autoScrollPreview = true;
    let lastPreviewScrollTop = 0;

    function stripTimeline(line) {
        let output = line.replace(/^\s*\[\d{2}:\d{2}:\d{2}(?:\.\d+)?\s*-->\s*\d{2}:\d{2}:\d{2}(?:\.\d+)?\]\s*/, '');
        output = output.replace(/^\s*\[[^\]]+\]\s*/, '');
        return output.trim();
    }

    function decodeEscapedText(raw) {
        if (raw === null || raw === undefined) return '';
        let text = String(raw).trim();
        for (let i = 0; i < 2; i += 1) {
            const wrapped = text.length >= 2 && text.startsWith('"') && text.endsWith('"');
            if (!wrapped) break;
            try {
                const parsed = JSON.parse(text);
                if (typeof parsed === 'string') {
                    text = parsed;
                    continue;
                }
            } catch (_) {}
            break;
        }
        return text.replace(/\\r\\n/g, '\n').replace(/\\n/g, '\n').replace(/\\r/g, '\n').replace(/\\t/g, '\t');
    }

    function toDisplayPreview(raw) {
        const decoded = decodeEscapedText(raw);
        if (!decoded) return '';
        const trimmed = String(decoded).trim();
        if (trimmed === '""' || trimmed === "''") return '';
        return decoded.split('\n').map(stripTimeline).filter(line => line && line !== '""' && line !== "''").join('\n');
    }

    function clampPercent(value) {
        return Math.max(0, Math.min(100, value || 0));
    }

    function renderState(phase, progress, preview, progressText) {
        currentPhase = phase || currentPhase || '대기 중';
        shownProgress = clampPercent(progress);
        shownPreview = preview || '';
        if (progressLabel) progressLabel.textContent = progressText || `${currentPhase} ${shownProgress}%`;
        if (progressBar) progressBar.style.width = `${shownProgress}%`;
        if (!livePreviewEl) return;

        const wasNearBottom = (livePreviewEl.scrollHeight - livePreviewEl.scrollTop - livePreviewEl.clientHeight) < 8;
        livePreviewEl.textContent = shownPreview;
        if (autoScrollPreview && wasNearBottom) {
            livePreviewEl.scrollTop = livePreviewEl.scrollHeight;
        }
    }

    function maybeRedirect() {
        if (!pendingRedirect || playbackRunning || snapshots.length >= 2) return;
        window.location.href = `/job/${jobId}`;
    }

    function runPlaybackIfPossible() {
        if (playbackRunning || snapshots.length < 2) {
            maybeRedirect();
            return;
        }
        playbackRunning = true;
        const from = snapshots[0];
        const to = snapshots[1];
        const baseDuration = Math.max(NORMAL_MIN_DURATION_MS, to.at - from.at);
        const overdueMs = Date.now() - to.at;
        const shouldCatchUp = snapshots.length > 2 || overdueMs > OVERDUE_THRESHOLD_MS;
        const duration = shouldCatchUp ? Math.max(FAST_MIN_DURATION_MS, Math.round(baseDuration / OVERDUE_SPEEDUP)) : baseDuration;
        const startedAt = performance.now();

        const tick = () => {
            if (!isPolling) {
                playbackRunning = false;
                return;
            }
            const elapsed = performance.now() - startedAt;
            const ratio = Math.max(0, Math.min(1, elapsed / duration));
            const progress = Math.round(from.progress + ((to.progress - from.progress) * ratio));
            const currentLen = Math.round(from.preview.length + ((to.preview.length - from.preview.length) * ratio));
            renderState(to.phase, progress, to.preview.slice(0, Math.max(0, currentLen)), to.progressLabel);

            if (ratio < 1) {
                setTimeout(tick, 40);
                return;
            }
            renderState(to.phase, to.progress, to.preview, to.progressLabel);
            snapshots.shift();
            playbackRunning = false;
            runPlaybackIfPossible();
        };
        tick();
    }

    function pushSnapshot(data) {
        const snapshot = {
            at: Date.now(),
            phase: data.phase || '대기 중',
            progress: clampPercent(data.progress_percent),
            preview: toDisplayPreview(data.preview_text || ''),
            progressLabel: data.progress_label || ''
        };
        const last = snapshots[snapshots.length - 1];
        if (last && last.phase === snapshot.phase && last.progress === snapshot.progress && last.preview === snapshot.preview) return;
        snapshots.push(snapshot);
        runPlaybackIfPossible();
    }

    function checkStatus() {
        if (!isPolling) return;
        fetch(`/status/${jobId}`)
            .then(response => response.json())
            .then(data => {
                if (!isPolling) return;
                statusEl.textContent = `상태: ${data.status}`;
                pushSnapshot(data);
                if (data.status === '정제 대기 중' || data.status === '정제 중' || data.status === '완료') {
                    pendingRedirect = true;
                    maybeRedirect();
                } else {
                    setTimeout(checkStatus, 3000);
                }
            })
            .catch(() => {
                if (isPolling) setTimeout(checkStatus, 5000);
            });
    }

    if (livePreviewEl) {
        livePreviewEl.addEventListener('scroll', () => {
            const currentTop = livePreviewEl.scrollTop;
            if (currentTop < lastPreviewScrollTop) autoScrollPreview = false;
            const nearBottom = (livePreviewEl.scrollHeight - livePreviewEl.scrollTop - livePreviewEl.clientHeight) < 24;
            if (nearBottom) autoScrollPreview = true;
            lastPreviewScrollTop = currentTop;
        });
    }

    snapshots.push({
        at: Date.now(),
        phase: currentPhase,
        progress: clampPercent(shownProgress),
        preview: toDisplayPreview(initialRawPreview),
        progressLabel: pageData.progress_label || ''
    });
    renderState(currentPhase, shownProgress, toDisplayPreview(initialRawPreview), pageData.progress_label || '');

    if (toggleBtn && detailPanel) {
        toggleBtn.addEventListener('click', () => {
            const opened = detailPanel.style.display !== 'none';
            detailPanel.style.display = opened ? 'none' : 'block';
            toggleBtn.textContent = opened ? '자세히 보기' : '간단히 보기';
        });
    }
    if (openModalBtn && modal) openModalBtn.addEventListener('click', () => { modal.style.display = 'flex'; });
    if (closeModalBtn && modal) closeModalBtn.addEventListener('click', () => { modal.style.display = 'none'; });
    if (modal) {
        modal.addEventListener('click', event => {
            if (event.target === modal) modal.style.display = 'none';
        });
    }

    setTimeout(checkStatus, 3000);
    window.addEventListener('beforeunload', () => { isPolling = false; });
})();
