/**
 * Whisper Server - File Explorer Application Logic (FINAL INTEGRATED VERSION)
 */

// --- 1. Global State ---
const state = {
    currentFolderID: '',
    viewMode: 'explore',
    currentPage: 1,
    totalPages: 1,
    currentVersion: '',
    allFolders: [],
    folderChildrenMap: {},
    lastDataFolders: [],
    lastDataJobs: [],
    selectedJobIDs: new Set(),
    selectedFolderIDs: new Set(),
    lastSelectedCard: null,
    pendingUploads: {},
    moveBrowseFolderID: '',
    blockedFolderIDs: new Set(),
    isDraggingBox: false,
    dragStartX: 0,
    dragStartY: 0,
    dragMoved: false,
    justFinishedDragging: false
};

let els = {};

// --- 2. Core Utilities (Defined at Top) ---

function initElements() {
    els = {
        form: document.getElementById('batch_form'),
        grid: document.getElementById('entries-grid'),
        empty: document.getElementById('empty-jobs'),
        pagination: document.getElementById('pagination'),
        searchControls: document.getElementById('search-controls'),
        selectionControls: document.getElementById('selection-controls'),
        selectionCount: document.getElementById('selection-count-label'),
        q: document.getElementById('searchQuery'),
        tag: document.getElementById('searchTag'),
        fileMenu: document.getElementById('file-action-menu'),
        workMenu: document.getElementById('workspace-action-menu'),
        moveModal: document.getElementById('move-modal'),
        uploadModal: document.getElementById('upload-setup-modal'),
        fileInput: document.getElementById('real-file-input'),
        uploadForm: document.getElementById('upload-setup-form'),
        moveConfirmBtn: document.getElementById('move-confirm-btn'),
        moveCancelBtn: document.getElementById('move-cancel-btn'),
        fileActionRename: document.getElementById('file-action-rename'),
        fileActionDownload: document.getElementById('file-action-download'),
        fileActionDelete: document.getElementById('file-action-delete')
    };
    if (els.form) {
        let box = document.querySelector('.selection-rect');
        if (!box) {
            box = document.createElement('div'); box.className = 'selection-rect'; box.style.display = 'none';
            els.form.appendChild(box);
        }
        els.selBox = box;
    }
}

function refreshAllFolders(raw) {
    state.allFolders = raw || [];
    state.folderChildrenMap = {};
    state.allFolders.forEach(f => {
        const p = (f.ParentID || '').trim();
        if (!state.folderChildrenMap[p]) state.folderChildrenMap[p] = [];
        state.folderChildrenMap[p].push(f);
    });
}

function isFormEmptyArea(target) {
    if (!target || !els.form || !els.form.contains(target)) return false;
    return !target.closest('.entry-card, .entry-row, .entry-menu-btn, a, button, input, select, textarea, label, .filter-panel, .filter-chip-btn');
}

function syncUI() {
    const total = state.selectedJobIDs.size + state.selectedFolderIDs.size;
    state.selectionMode = total > 0;
    if (els.selectionControls) els.selectionControls.style.display = state.selectionMode ? 'flex' : 'none';
    if (els.searchControls) els.searchControls.style.display = state.selectionMode ? 'none' : 'flex';
    if (els.selectionCount) els.selectionCount.textContent = `${total}개 항목 선택됨`;
    if (els.grid) els.grid.classList.toggle('selection-mode', state.selectionMode);

    document.querySelectorAll('.entry-card, .entry-row').forEach(card => {
        const id = (card.dataset.jobId || card.dataset.folderId || '').trim();
        if (!id) return;
        const isF = card.dataset.type === 'folder';
        const isSelected = isF ? state.selectedFolderIDs.has(id) : state.selectedJobIDs.has(id);
        card.classList.toggle('is-selected', isSelected);
        const cb = card.querySelector('input.entry-check');
        if (cb) cb.checked = isSelected;
    });
}

function setCardSelected(card, on) {
    if (!card) return;
    const id = (card.dataset.jobId || card.dataset.folderId || '').trim();
    if (!id) return;
    const isF = card.dataset.type === 'folder';
    if (on) { if (isF) state.selectedFolderIDs.add(id); else state.selectedJobIDs.add(id); }
    else { if (isF) state.selectedFolderIDs.delete(id); else state.selectedJobIDs.delete(id); }
}

function clearSelection() {
    state.selectedJobIDs.clear(); state.selectedFolderIDs.clear(); state.lastSelectedCard = null;
    syncUI();
}

function toggleCard(card, multi, shift) {
    if (!card) return;
    const id = (card.dataset.jobId || card.dataset.folderId || '').trim();
    const isF = card.dataset.type === 'folder';
    const targetSet = isF ? state.selectedFolderIDs : state.selectedJobIDs;
    const isSelected = targetSet.has(id);

    if (shift && state.lastSelectedCard) {
        const all = Array.from(document.querySelectorAll('.file-entry, .entry-card[data-type="folder"]'));
        const s = all.indexOf(state.lastSelectedCard), e = all.indexOf(card);
        const [low, high] = s < e ? [s, e] : [e, s];
        for (let i = low; i <= high; i++) {
            const c = all[i], cid = (c.dataset.jobId || c.dataset.folderId || '').trim();
            if (c.dataset.type === 'folder') state.selectedFolderIDs.add(cid); else state.selectedJobIDs.add(cid);
        }
    } else if (multi) {
        if (isSelected) setCardSelected(card, false); else setCardSelected(card, true);
    } else {
        if (!isSelected) { clearSelection(); setCardSelected(card, true); }
    }
    state.lastSelectedCard = card;
    syncUI();
}

// --- 3. Window Global Actions ---

window.triggerFileUpload = function() { if (els.fileInput) els.fileInput.click(); };
window.closeUploadModal = function() { if (els.uploadModal) els.uploadModal.classList.remove('show'); if (els.fileInput) els.fileInput.value = ''; };
window.navigateFolder = function(id) { state.currentFolderID = (id || '').trim(); state.currentPage = 1; clearSelection(); pollJobs(true); };

window.submitDownload = function() {
    const ids = Array.from(state.selectedJobIDs); if (ids.length === 0) return;
    const f = document.createElement('form'); f.method = 'POST'; f.action = '/batch-download';
    ids.forEach(id => { const i = document.createElement('input'); i.type = 'hidden'; i.name = 'job_ids'; i.value = id; f.appendChild(i); });
    document.body.appendChild(f); f.submit(); f.remove();
};

window.confirmTrash = function() {
    const j = Array.from(state.selectedJobIDs), f = Array.from(state.selectedFolderIDs);
    if (j.length + f.length === 0) return;
    if (!confirm('삭제하시겠습니까?')) return;
    const fd = new URLSearchParams();
    j.forEach(id => fd.append('job_ids', id)); f.forEach(id => fd.append('folder_ids', id));
    fetch('/batch-delete', { method: 'POST', body: fd, headers: { 'Content-Type': 'application/x-www-form-urlencoded' } }).then(r => { if (r.ok) { clearSelection(); pollJobs(true); } });
};

window.openMoveDialog = function() {
    state.moveBrowseFolderID = state.currentFolderID; state.blockedFolderIDs = new Set(state.selectedFolderIDs);
    if (els.moveModal) { els.moveModal.classList.add('show'); renderMoveBrowser(); }
};

window.closeMoveDialog = function() { if (els.moveModal) els.moveModal.classList.remove('show'); };

window.submitMove = function() {
    const fd = new URLSearchParams(); fd.append('target_folder_id', (state.moveBrowseFolderID || '').trim());
    state.selectedJobIDs.forEach(id => fd.append('job_ids', id)); state.selectedFolderIDs.forEach(id => fd.append('folder_ids', id));
    fetch('/batch-move', { method: 'POST', body: fd, headers: { 'Content-Type': 'application/x-www-form-urlencoded' } }).then(r => { if (r.ok) { window.closeMoveDialog(); clearSelection(); pollJobs(true); } });
};

window.createNewFolder = function() {
    const n = prompt('새 폴더 이름'); if (!n) return;
    fetch('/folders', { method: 'POST', body: new URLSearchParams({ folder_name: n.trim(), parent_id: state.currentFolderID }), headers: { 'Content-Type': 'application/x-www-form-urlencoded' } }).then(r => { if (r.ok) pollJobs(true); });
};

// --- 4. Rendering ---

function pollJobs(force = false) {
    const q = els.q ? encodeURIComponent(els.q.value) : '', t = els.tag ? encodeURIComponent(els.tag.value) : '';
    fetch(`/jobs/updates?q=${q}&tag=${t}&folder=${state.currentFolderID}&view=${state.viewMode}&page=${state.currentPage}&v=${force ? '' : state.currentVersion}`)
        .then(r => r.ok ? r.json() : null)
        .then(data => {
            if (!data) return;
            state.totalPages = data.total_pages || 1;
            if (data.changed === false && !force) return;
            state.currentVersion = data.version;
            if (data.all_folders) refreshAllFolders(data.all_folders);
            if (data.folder_path) renderBreadcrumbs(data.folder_path);
            state.lastDataFolders = data.folder_items || [];
            state.lastDataJobs = data.job_items || [];
            renderEntries();
        });
}

function renderBreadcrumbs(path) {
    const titleEl = document.querySelector('.content-title'); if (!titleEl || state.viewMode === 'home') return;
    titleEl.innerHTML = '';
    const create = (id, name) => {
        const a = document.createElement('a'); a.className = 'crumb-link crumb-drop-target'; a.dataset.folderId = id; a.textContent = name;
        a.onclick = e => { e.preventDefault(); window.navigateFolder(id); }; return a;
    };
    titleEl.appendChild(create('', '내 파일'));
    (path || []).forEach(n => {
        const s = document.createElement('span'); s.className = 'crumb-sep'; s.textContent = '>'; titleEl.appendChild(s);
        titleEl.appendChild(create(n.ID, n.Name));
    });
}

function renderEntries() {
    if (!els.grid) return;
    els.grid.innerHTML = '';
    if (state.viewMode === 'home') {
        state.lastDataJobs.forEach(j => els.grid.appendChild(createJobCard(j, true)));
    } else {
        const fg = document.createElement('div'); fg.id='folder-grid'; fg.className = 'fs-grid folder-grid';
        const fl = document.createElement('div'); fl.id='file-grid'; fl.className = 'fs-grid file-grid';
        state.lastDataFolders.forEach(f => fg.appendChild(createFolderCard(f)));
        state.lastDataJobs.forEach(j => fl.appendChild(createJobCard(j, false)));
        Object.values(state.pendingUploads).filter(u => u.folderID === state.currentFolderID).forEach(u => {
            const c = document.createElement('article'); c.className = 'entry-card file-entry is-uploading';
            c.innerHTML = `<div class="entry-title">📄 ${u.filename}</div><div class="entry-sub">업로드 중...</div>
                           <div class="upload-progress-container"><div class="upload-progress-circle" style="--progress:${u.progress}"></div></div>`;
            fl.appendChild(c);
        });
        if (fg.children.length > 0) els.grid.appendChild(fg);
        if (fl.children.length > 0) els.grid.appendChild(fl);
    }
    if (els.grid.children.length === 0 && els.empty) els.empty.style.display = 'block'; else if (els.empty) els.empty.style.display = 'none';
    syncUI();
    bindDragAndDropHandlers();
    renderPagination();
}

function createFolderCard(f) {
    const c = document.createElement('article'); c.className = 'entry-card is-folder folder-drop-target';
    c.dataset.type = 'folder'; c.dataset.folderId = f.ID; c.setAttribute('draggable', 'true');
    c.innerHTML = `<div class="entry-actions"><button type="button" class="entry-menu-btn">⋮</button></div>
                   <input class="entry-check" type="checkbox" name="folder_ids" value="${f.ID}">
                   <div class="entry-title">📁 ${f.Name}</div>`;
    return c;
}

function createJobCard(j, isRow) {
    const c = document.createElement('article'); c.className = isRow ? 'entry-row file-entry' : 'entry-card file-entry';
    c.dataset.type = 'file'; c.dataset.jobId = j.ID; c.dataset.openUrl = `/job/${j.ID}`; c.setAttribute('draggable', 'true');
    c.innerHTML = `<div class="entry-actions"><button type="button" class="entry-menu-btn">⋮</button></div>
                   <div style="display:flex;align-items:flex-start;gap:8px;">
                       <label style="display:flex;gap:6px;align-items:flex-start;flex:1;cursor:pointer;">
                           <input class="entry-check" type="checkbox" name="job_ids" value="${j.ID}">
                           <div><div class="entry-title">📄 ${j.Filename}</div><div class="entry-sub">${j.MediaDuration}</div></div>
                       </label>
                   </div><div class="status" style="font-size:.84em;margin-top:4px;">${j.Status}</div>`;
    return c;
}

function renderPagination() {
    if (!els.pagination) return;
    els.pagination.innerHTML = '';
    if (state.totalPages <= 1) return;
    for (let i = 1; i <= state.totalPages; i++) {
        const btn = document.createElement('button'); btn.className = `page-btn ${i === state.currentPage ? 'active' : ''}`;
        btn.textContent = i; btn.onclick = () => { state.currentPage = i; pollJobs(true); };
        els.pagination.appendChild(btn);
    }
}

function renderMoveBrowser() {
    const listEl = document.getElementById('move-browser-list'), nameEl = document.getElementById('move-current-chip-name');
    if (!listEl) return; listEl.innerHTML = '';
    const children = state.folderChildrenMap[state.moveBrowseFolderID || ''] || [];
    if (children.length === 0) listEl.innerHTML = '<div class="move-folder-empty">비어 있음</div>';
    children.forEach(f => {
        if (state.blockedFolderIDs.has(f.ID)) return;
        const r = document.createElement('div'); r.className = 'move-folder-row';
        r.innerHTML = `<button class="move-folder-cell">📁 ${f.Name}</button><button class="move-folder-go">열기</button>`;
        r.querySelectorAll('button').forEach(b => b.onclick = () => { state.moveBrowseFolderID = f.ID; renderMoveBrowser(); });
        listEl.appendChild(r);
    });
    const curr = state.allFolders.find(f => f.ID === state.moveBrowseFolderID);
    if (nameEl) nameEl.textContent = curr ? curr.Name : '내 파일';
}

function bindGlobalEvents() {
    document.addEventListener('click', e => {
        const target = e.target, card = target.closest('.entry-card, .entry-row'), menuBtn = target.closest('.entry-menu-btn');
        if (!target.closest('#file-action-menu, .entry-menu-btn')) els.fileMenu?.classList.remove('show');
        if (!target.closest('#workspace-action-menu')) els.workMenu?.classList.remove('show');
        if (menuBtn) {
            e.preventDefault(); e.stopPropagation();
            const c = menuBtn.closest('.entry-card, .entry-row');
            if (!c.classList.contains('is-selected')) { clearSelection(); setCardSelected(c, true); syncUI(); }
            const r = menuBtn.getBoundingClientRect(); els.fileMenu.style.left = r.left + 'px'; els.fileMenu.style.top = (r.bottom + 4) + 'px'; els.fileMenu.classList.add('show');
            return;
        }
        if (card && !target.closest('a, button')) { if (target.closest('label')) e.preventDefault(); toggleCard(card, e.ctrlKey || e.metaKey, e.shiftKey); return; }
        if (isFormEmptyArea(target) && !state.justFinishedDragging) clearSelection();
    });

    document.addEventListener('dblclick', e => {
        const card = e.target.closest('.entry-card, .entry-row');
        if (card && !e.target.closest('a, button, input, .entry-menu-btn')) {
            e.preventDefault(); e.stopPropagation();
            if (card.dataset.type === 'folder') window.navigateFolder(card.dataset.folderId);
            else if (card.dataset.openUrl) window.location.href = card.dataset.openUrl;
        }
    });

    document.addEventListener('contextmenu', e => {
        const card = e.target.closest('.entry-card, .entry-row');
        if (card) {
            e.preventDefault();
            if (!card.classList.contains('is-selected')) { clearSelection(); setCardSelected(card, true); syncUI(); }
            els.fileMenu.style.left = e.clientX + 'px'; els.fileMenu.style.top = e.clientY + 'px'; els.fileMenu.classList.add('show');
        } else if (isFormEmptyArea(e.target)) {
            e.preventDefault(); els.workMenu.style.left = e.clientX + 'px'; els.workMenu.style.top = e.clientY + 'px'; els.workMenu.classList.add('show');
        }
    });

    if (els.uploadForm) {
        els.uploadForm.onsubmit = e => {
            e.preventDefault();
            const fd = new FormData(els.uploadForm), f = els.fileInput.files[0], id = 'up-' + Date.now();
            state.pendingUploads[id] = { id, filename: fd.get('display_name') || f.name, progress: 0, folderID: fd.get('folder_id') };
            window.closeUploadModal(); renderEntries();
            const xhr = new XMLHttpRequest(); xhr.open('POST', '/upload');
            xhr.upload.onprogress = ev => { if (ev.lengthComputable) { state.pendingUploads[id].progress = Math.round((ev.loaded/ev.total)*100); renderEntries(); } };
            xhr.onload = () => { delete state.pendingUploads[id]; pollJobs(true); };
            xhr.send(fd);
        };
    }

    els.fileActionRename?.addEventListener('click', () => {
        const id = Array.from(state.selectedJobIDs)[0] || Array.from(state.selectedFolderIDs)[0];
        const isF = state.selectedFolderIDs.size > 0;
        const n = prompt('새 이름을 입력하세요');
        if (n) fetch(isF ? `/folders/${id}/rename` : `/job/${id}/rename`, {method:'POST', body:new URLSearchParams({new_name:n}), headers:{'Content-Type':'application/x-www-form-urlencoded'}}).then(()=>pollJobs(true));
        els.fileMenu?.classList.remove('show');
    });
}

function bindDragAndDropHandlers() {
    document.querySelectorAll('#entries-grid [draggable="true"]').forEach(c => {
        c.ondragstart = e => {
            if (!c.classList.contains('is-selected')) { clearSelection(); setCardSelected(c, true); syncUI(); }
            e.dataTransfer.setData('application/json', JSON.stringify({ job_ids: Array.from(state.selectedJobIDs), folder_ids: Array.from(state.selectedFolderIDs) }));
            e.dataTransfer.effectAllowed = 'move';
        };
    });
    document.querySelectorAll('.folder-drop-target, .crumb-drop-target').forEach(t => {
        t.ondragover = e => { e.preventDefault(); t.classList.add('drop-highlight'); };
        t.ondragleave = () => t.classList.remove('drop-highlight');
        t.ondrop = e => {
            e.preventDefault(); t.classList.remove('drop-highlight');
            try {
                const p = JSON.parse(e.dataTransfer.getData('application/json'));
                const targetId = (t.dataset.folderId || '').trim();
                if (p.folder_ids.includes(targetId)) return;
                const fd = new URLSearchParams({ target_folder_id: targetId });
                p.job_ids.forEach(id => fd.append('job_ids', id)); p.folder_ids.forEach(id => fd.append('folder_ids', id));
                fetch('/batch-move', {method:'POST', body:fd, headers: { 'Content-Type': 'application/x-www-form-urlencoded' }}).then(()=>pollJobs(true));
            } catch(_) {}
        };
    });
}

function initDragBox() {
    let sX, sY;
    document.addEventListener('mousedown', e => {
        if (e.button !== 0 || !isFormEmptyArea(e.target)) return;
        const r = els.form.getBoundingClientRect();
        sX = e.clientX - r.left + els.form.scrollLeft; sY = e.clientY - r.top + els.form.scrollTop;
        state.isDraggingBox = true; state.dragMoved = false;
        if (!e.ctrlKey && !e.metaKey && !e.shiftKey) clearSelection();
        els.selBox.style.width = '0'; els.selBox.style.height = '0'; els.selBox.style.left = sX+'px'; els.selBox.style.top = sY+'px';
        els.selBox.style.display = 'block';
        document.body.classList.add('is-selecting');
    });
    document.addEventListener('mousemove', e => {
        if (!state.isDraggingBox) return;
        const r = els.form.getBoundingClientRect();
        const curX = Math.max(0, Math.min(e.clientX - r.left + els.form.scrollLeft, els.form.scrollWidth));
        const curY = Math.max(0, Math.min(e.clientY - r.top + els.form.scrollTop, els.form.scrollHeight));
        const l = Math.min(sX, curX), t = Math.min(sY, curY), w = Math.abs(sX-curX), h = Math.abs(sY-curY);
        els.selBox.style.left = l+'px'; els.selBox.style.top = t+'px'; els.selBox.style.width = w+'px'; els.selBox.style.height = h+'px';
        if (w > 5 || h > 5) {
            state.dragMoved = true;
            const box = { left: l+r.left-els.form.scrollLeft, top: t+r.top-els.form.scrollTop, right: l+w+r.left-els.form.scrollLeft, bottom: t+h+r.top-els.form.scrollTop };
            document.querySelectorAll('.entry-card, .entry-row').forEach(item => {
                const cr = item.getBoundingClientRect();
                if (!(cr.right < box.left || cr.left > box.right || cr.bottom < box.top || cr.top > box.bottom)) setCardSelected(item, true);
            });
            syncUI();
        }
    });
    document.addEventListener('mouseup', () => { if (!state.isDraggingBox) return; state.isDraggingBox = false; els.selBox.style.display = 'none'; document.body.classList.remove('is-selecting'); state.justFinishedDragging = true; setTimeout(() => state.justFinishedDragging = false, 100); });
}

window.initApp = function(config) {
    initElements();
    Object.assign(state, config);
    if (config.allFolders) refreshAllFolders(config.allFolders);
    bindGlobalEvents();
    initDragBox();
    pollJobs(true);
    setInterval(() => pollJobs(false), 3000);
};
