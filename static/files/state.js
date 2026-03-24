/**
 * Whisper Server - File Explorer Shared State
 */

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
        uploadFileDisplayName: document.getElementById('upload-file-display-name'),
        uploadFilename: document.getElementById('upload-filename'),
        uploadFolderID: document.getElementById('upload-folder-id'),
        uploadTag: document.getElementById('upload-tag'),
        uploadDesc: document.getElementById('upload-desc'),
        moveConfirmBtn: document.getElementById('move-confirm-btn'),
        moveCancelBtn: document.getElementById('move-cancel-btn'),
        moveUpBtn: document.getElementById('move-up-btn'),
        movePath: document.getElementById('move-browser-path'),
        cancelSelectionBtn: document.getElementById('cancel-selection-btn'),
        fileActionRename: document.getElementById('file-action-rename'),
        fileActionDownload: document.getElementById('file-action-download'),
        fileActionDelete: document.getElementById('file-action-delete'),
        workActionNewFolder: document.getElementById('workspace-action-new-folder'),
        workActionUpload: document.getElementById('workspace-action-upload'),
        sortFilterToggle: document.getElementById('sort-filter-toggle'),
        sortFilterPanel: document.getElementById('sort-filter-panel'),
        searchSort: document.getElementById('searchSort'),
        searchOrder: document.getElementById('searchOrder'),
        homeFoldersGrid: document.getElementById('home-folders-grid'),
        homeFilesList: document.getElementById('home-files-list')
    };
    if (els.form) {
        let box = document.querySelector('.selection-rect');
        if (!box) {
            box = document.createElement('div');
            box.className = 'selection-rect';
            box.style.display = 'none';
            els.form.appendChild(box);
        }
        els.selBox = box;
    }
}

function refreshAllFolders(raw) {
    state.allFolders = raw || [];
    state.folderChildrenMap = {};
    state.allFolders.forEach(f => {
        const parentID = (f.ParentID || '').trim();
        if (!state.folderChildrenMap[parentID]) {
            state.folderChildrenMap[parentID] = [];
        }
        state.folderChildrenMap[parentID].push(f);
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
        const isFolder = card.dataset.type === 'folder';
        const isSelected = isFolder ? state.selectedFolderIDs.has(id) : state.selectedJobIDs.has(id);
        card.classList.toggle('is-selected', isSelected);
        const checkbox = card.querySelector('input.entry-check');
        if (checkbox) checkbox.checked = isSelected;
    });
}

function setCardSelected(card, on) {
    if (!card) return;
    const id = (card.dataset.jobId || card.dataset.folderId || '').trim();
    if (!id) return;
    const isFolder = card.dataset.type === 'folder';
    if (on) {
        if (isFolder) state.selectedFolderIDs.add(id);
        else state.selectedJobIDs.add(id);
    } else {
        if (isFolder) state.selectedFolderIDs.delete(id);
        else state.selectedJobIDs.delete(id);
    }
}

function clearSelection() {
    state.selectedJobIDs.clear();
    state.selectedFolderIDs.clear();
    state.lastSelectedCard = null;
    syncUI();
}

function toggleCard(card, multi, shift) {
    if (!card) return;
    const id = (card.dataset.jobId || card.dataset.folderId || '').trim();
    const isFolder = card.dataset.type === 'folder';
    const targetSet = isFolder ? state.selectedFolderIDs : state.selectedJobIDs;
    const isSelected = targetSet.has(id);

    if (shift && state.lastSelectedCard) {
        const all = Array.from(document.querySelectorAll('.file-entry, .entry-card[data-type="folder"]'));
        const start = all.indexOf(state.lastSelectedCard);
        const end = all.indexOf(card);
        const [low, high] = start < end ? [start, end] : [end, start];
        for (let i = low; i <= high; i += 1) {
            const current = all[i];
            const currentID = (current.dataset.jobId || current.dataset.folderId || '').trim();
            if (current.dataset.type === 'folder') state.selectedFolderIDs.add(currentID);
            else state.selectedJobIDs.add(currentID);
        }
    } else if (multi) {
        setCardSelected(card, !isSelected);
    } else if (!isSelected) {
        clearSelection();
        setCardSelected(card, true);
    }

    state.lastSelectedCard = card;
    syncUI();
}

window.triggerFileUpload = function () { if (els.fileInput) els.fileInput.click(); };
window.closeUploadModal = function () {
    if (els.uploadModal) els.uploadModal.classList.remove('show');
    if (els.uploadForm) els.uploadForm.reset();
    if (els.fileInput) els.fileInput.value = '';
    if (els.uploadFolderID) els.uploadFolderID.value = state.currentFolderID || '';
    if (els.uploadFileDisplayName) els.uploadFileDisplayName.textContent = '';
};
window.navigateFolder = function (id) {
    const trimmed = (id || '').trim();
    window.location.href = trimmed ? `/files/folders/${trimmed}` : '/files/root';
};

window.submitDownload = function () {
    const ids = Array.from(state.selectedJobIDs);
    if (ids.length === 0) return;
    const form = document.createElement('form');
    form.method = 'POST';
    form.action = '/batch-download';
    ids.forEach(id => {
        const input = document.createElement('input');
        input.type = 'hidden';
        input.name = 'job_ids';
        input.value = id;
        form.appendChild(input);
    });
    document.body.appendChild(form);
    form.submit();
    form.remove();
};

window.confirmTrash = function () {
    const jobIDs = Array.from(state.selectedJobIDs);
    const folderIDs = Array.from(state.selectedFolderIDs);
    if (jobIDs.length + folderIDs.length === 0 || !confirm('삭제하시겠습니까?')) return;
    const formData = new URLSearchParams();
    jobIDs.forEach(id => formData.append('job_ids', id));
    folderIDs.forEach(id => formData.append('folder_ids', id));
    fetch('/batch-delete', { method: 'POST', body: formData, headers: { 'Content-Type': 'application/x-www-form-urlencoded' } })
        .then(response => { if (response.ok) { clearSelection(); pollJobs(true); } });
};

window.openMoveDialog = function () {
    state.moveBrowseFolderID = state.currentFolderID;
    state.blockedFolderIDs = new Set(state.selectedFolderIDs);
    if (els.moveModal) {
        els.moveModal.classList.add('show');
        renderMoveBrowser();
    }
};

window.closeMoveDialog = function () { if (els.moveModal) els.moveModal.classList.remove('show'); };

window.submitMove = function () {
    const formData = new URLSearchParams();
    formData.append('target_folder_id', (state.moveBrowseFolderID || '').trim());
    state.selectedJobIDs.forEach(id => formData.append('job_ids', id));
    state.selectedFolderIDs.forEach(id => formData.append('folder_ids', id));
    fetch('/batch-move', { method: 'POST', body: formData, headers: { 'Content-Type': 'application/x-www-form-urlencoded' } })
        .then(response => { if (response.ok) { window.closeMoveDialog(); clearSelection(); pollJobs(true); } });
};

window.createNewFolder = function () {
    const name = prompt('새 폴더 이름');
    if (!name) return;
    fetch('/folders', {
        method: 'POST',
        body: new URLSearchParams({ folder_name: name.trim(), parent_id: state.currentFolderID }),
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' }
    }).then(response => { if (response.ok) pollJobs(true); });
};
