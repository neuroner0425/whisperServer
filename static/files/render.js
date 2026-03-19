/**
 * Whisper Server - File Explorer Rendering
 */

function pollJobs(force = false) {
    const q = els.q ? encodeURIComponent(els.q.value) : '';
    const tag = els.tag ? encodeURIComponent(els.tag.value) : '';
    const sort = els.searchSort ? els.searchSort.value : '';
    const order = els.searchOrder ? els.searchOrder.value : '';
    fetch(`/jobs/updates?q=${q}&tag=${tag}&folder=${state.currentFolderID}&view=${state.viewMode}&page=${state.currentPage}&sort=${sort}&order=${order}&v=${force ? '' : state.currentVersion}`)
        .then(response => response.ok ? response.json() : null)
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
    const titleEl = document.querySelector('.content-title');
    if (!titleEl || state.viewMode === 'home') return;
    titleEl.innerHTML = '';
    const createCrumb = (id, name) => {
        const link = document.createElement('a');
        link.className = 'crumb-link crumb-drop-target';
        link.dataset.folderId = id;
        link.textContent = name;
        link.onclick = event => {
            event.preventDefault();
            window.navigateFolder(id);
        };
        return link;
    };
    titleEl.appendChild(createCrumb('', '내 파일'));
    (path || []).forEach(node => {
        const separator = document.createElement('span');
        separator.className = 'crumb-sep';
        separator.textContent = '>';
        titleEl.appendChild(separator);
        titleEl.appendChild(createCrumb(node.ID, node.Name));
    });
}

function renderEntries() {
    if (state.viewMode === 'home') {
        renderHomeEntries();
    } else if (els.grid) {
        renderExplorerEntries();
    }
    syncUI();
    bindDragAndDropHandlers();
    renderPagination();
}

function renderHomeEntries() {
    if (els.homeFoldersGrid) {
        els.homeFoldersGrid.innerHTML = '';
        state.lastDataFolders.forEach(folder => els.homeFoldersGrid.appendChild(createHomeFolderCard(folder)));
    }
    if (els.homeFilesList) {
        els.homeFilesList.innerHTML = '';
        state.lastDataJobs.forEach(job => els.homeFilesList.appendChild(createHomeFileRow(job)));
    }
    const hasItems = state.lastDataFolders.length > 0 || state.lastDataJobs.length > 0;
    if (els.empty) els.empty.style.display = hasItems ? 'none' : 'block';
}

function renderExplorerEntries() {
    els.grid.innerHTML = '';
    const folderGrid = document.createElement('div');
    folderGrid.id = 'folder-grid';
    folderGrid.className = 'fs-grid folder-grid';
    const fileGrid = document.createElement('div');
    fileGrid.id = 'file-grid';
    fileGrid.className = 'fs-grid file-grid';

    state.lastDataFolders.forEach(folder => folderGrid.appendChild(createFolderCard(folder)));
    state.lastDataJobs.forEach(job => fileGrid.appendChild(createJobCard(job, false)));
    Object.values(state.pendingUploads)
        .filter(upload => upload.folderID === state.currentFolderID)
        .forEach(upload => fileGrid.appendChild(createPendingUploadCard(upload)));

    if (folderGrid.children.length > 0) els.grid.appendChild(folderGrid);
    if (fileGrid.children.length > 0) els.grid.appendChild(fileGrid);
    if (els.empty) els.empty.style.display = els.grid.children.length === 0 ? 'block' : 'none';
}

function createPendingUploadCard(upload) {
    const card = document.createElement('article');
    card.className = 'entry-card file-entry is-uploading';
    card.innerHTML = `<div class="entry-title">📄 ${upload.filename}</div><div class="entry-sub">업로드 중...</div>
        <div class="upload-progress-container"><div class="upload-progress-circle" style="--progress:${upload.progress}"></div></div>`;
    return card;
}

function createHomeFolderCard(folder) {
    const card = document.createElement('article');
    card.className = 'entry-card is-folder home-folder-card';
    card.dataset.type = 'folder';
    card.dataset.folderId = folder.ID;
    card.dataset.openUrl = `/files/folders/${folder.ID}`;
    card.innerHTML = `<div class="entry-actions"><button type="button" class="entry-menu-btn">⋮</button></div>
        <div class="home-folder-content">
            <span class="home-folder-icon">📁</span>
            <div class="home-folder-info">
                <div class="entry-title">${folder.Name}</div>
                <div class="entry-sub">위치: 내 드라이브</div>
            </div>
        </div>`;
    return card;
}

function createHomeFileRow(job) {
    const row = document.createElement('article');
    row.className = 'home-file-row entry-row file-entry';
    row.dataset.type = 'file';
    row.dataset.jobId = job.ID;
    row.dataset.openUrl = `/job/${job.ID}`;
    const date = (job.UpdatedAt || '').split(' ')[0] || '';
    row.innerHTML = `<div class="col-name"><span class="file-icon">📄</span> <span class="entry-title">${job.Filename}</span></div>
        <div class="col-date">${date}</div>
        <div class="col-location"><span class="loc-icon">📁</span> ${job.FolderName || '내 파일'}</div>
        <div class="col-action"><button type="button" class="entry-menu-btn">⋮</button></div>`;
    return row;
}

function createFolderCard(folder) {
    const card = document.createElement('article');
    card.className = 'entry-card is-folder folder-drop-target';
    card.dataset.type = 'folder';
    card.dataset.folderId = folder.ID;
    card.setAttribute('draggable', 'true');
    card.innerHTML = `<div class="entry-actions"><button type="button" class="entry-menu-btn">⋮</button></div>
        <input class="entry-check" type="checkbox" name="folder_ids" value="${folder.ID}">
        <div class="entry-title">📁 ${folder.Name}</div>`;
    return card;
}

function createJobCard(job, isRow) {
    const card = document.createElement('article');
    card.className = isRow ? 'entry-row file-entry' : 'entry-card file-entry';
    card.dataset.type = 'file';
    card.dataset.jobId = job.ID;
    card.dataset.openUrl = `/job/${job.ID}`;
    card.setAttribute('draggable', 'true');
    card.innerHTML = `<div class="entry-actions"><button type="button" class="entry-menu-btn">⋮</button></div>
        <div style="display:flex;align-items:flex-start;gap:8px;">
            <label style="display:flex;gap:6px;align-items:flex-start;flex:1;cursor:pointer;">
                <input class="entry-check" type="checkbox" name="job_ids" value="${job.ID}">
                <div><div class="entry-title">📄 ${job.Filename}</div><div class="entry-sub">${job.MediaDuration}</div></div>
            </label>
        </div><div class="status" style="font-size:.84em;margin-top:4px;">${job.Status}</div>`;
    return card;
}

function renderPagination() {
    if (!els.pagination) return;
    els.pagination.innerHTML = '';
    if (state.totalPages <= 1) return;
    for (let i = 1; i <= state.totalPages; i += 1) {
        const btn = document.createElement('button');
        btn.className = `page-btn ${i === state.currentPage ? 'active' : ''}`;
        btn.textContent = i;
        btn.onclick = () => {
            state.currentPage = i;
            pollJobs(true);
        };
        els.pagination.appendChild(btn);
    }
}

function renderMoveBrowser() {
    const listEl = document.getElementById('move-browser-list');
    const nameEl = document.getElementById('move-current-chip-name');
    if (!listEl) return;
    listEl.innerHTML = '';
    const children = state.folderChildrenMap[state.moveBrowseFolderID || ''] || [];
    if (children.length === 0) listEl.innerHTML = '<div class="move-folder-empty">비어 있음</div>';

    children.forEach(folder => {
        if (state.blockedFolderIDs.has(folder.ID)) return;
        const dateStr = (folder.UpdatedAt || '').split(' ')[0] || '-';
        const row = document.createElement('div');
        row.className = 'move-folder-row';
        row.innerHTML = `<button class="move-folder-cell">📁 ${folder.Name}</button>
            <span class="move-folder-date">${dateStr}</span>
            <button class="move-folder-go">열기</button>`;
        row.querySelectorAll('button').forEach(button => {
            button.onclick = () => {
                state.moveBrowseFolderID = folder.ID;
                renderMoveBrowser();
            };
        });
        listEl.appendChild(row);
    });

    const current = state.allFolders.find(folder => folder.ID === state.moveBrowseFolderID);
    if (nameEl) nameEl.textContent = current ? current.Name : '내 파일';
    renderMovePath();
}

function renderMovePath() {
    if (!els.movePath) return;
    els.movePath.innerHTML = '';
    const createCrumb = (id, name) => {
        const btn = document.createElement('button');
        btn.className = 'move-crumb';
        btn.textContent = name;
        btn.onclick = () => {
            state.moveBrowseFolderID = id;
            renderMoveBrowser();
        };
        return btn;
    };
    els.movePath.appendChild(createCrumb('', '내 파일'));
    buildMovePath(state.moveBrowseFolderID).forEach(node => {
        const separator = document.createElement('span');
        separator.className = 'move-path-sep';
        separator.textContent = '>';
        els.movePath.appendChild(separator);
        els.movePath.appendChild(createCrumb(node.ID, node.Name));
    });
}

function buildMovePath(id) {
    const path = [];
    let current = state.allFolders.find(folder => folder.ID === id);
    while (current) {
        path.unshift(current);
        current = state.allFolders.find(folder => folder.ID === (current.ParentID || '').trim());
    }
    return path;
}
