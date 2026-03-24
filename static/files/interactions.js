function bindGlobalEvents() {
    document.addEventListener('click', event => {
        const target = event.target;
        const card = target.closest('.entry-card, .entry-row');
        const menuBtn = target.closest('.entry-menu-btn');
        if (!target.closest('#file-action-menu, .entry-menu-btn')) els.fileMenu?.classList.remove('show');
        if (!target.closest('#workspace-action-menu')) els.workMenu?.classList.remove('show');

        if (menuBtn) {
            event.preventDefault();
            event.stopPropagation();
            const parentCard = menuBtn.closest('.entry-card, .entry-row');
            if (!parentCard.classList.contains('is-selected')) {
                clearSelection();
                setCardSelected(parentCard, true);
                syncUI();
            }
            const rect = menuBtn.getBoundingClientRect();
            els.fileMenu.style.left = `${rect.left}px`;
            els.fileMenu.style.top = `${rect.bottom + 4}px`;
            els.fileMenu.classList.add('show');
            return;
        }

        if (card && !target.closest('a, button')) {
            if (target.closest('label')) event.preventDefault();
            toggleCard(card, event.ctrlKey || event.metaKey, event.shiftKey);
            return;
        }
        if (isFormEmptyArea(target) && !state.justFinishedDragging) clearSelection();
    });

    document.addEventListener('dblclick', event => {
        const card = event.target.closest('.entry-card, .entry-row');
        if (!card || event.target.closest('a, button, input, .entry-menu-btn')) return;
        event.preventDefault();
        event.stopPropagation();
        if (card.dataset.type === 'folder') window.navigateFolder(card.dataset.folderId);
        else if (card.dataset.openUrl) window.location.href = card.dataset.openUrl;
    });

    document.addEventListener('contextmenu', event => {
        const card = event.target.closest('.entry-card, .entry-row');
        if (card) {
            event.preventDefault();
            if (!card.classList.contains('is-selected')) {
                clearSelection();
                setCardSelected(card, true);
                syncUI();
            }
            els.fileMenu.style.left = `${event.clientX}px`;
            els.fileMenu.style.top = `${event.clientY}px`;
            els.fileMenu.classList.add('show');
        } else if (isFormEmptyArea(event.target)) {
            event.preventDefault();
            els.workMenu.style.left = `${event.clientX}px`;
            els.workMenu.style.top = `${event.clientY}px`;
            els.workMenu.classList.add('show');
        }
    });

    bindUploadEvents();
    bindActionMenuEvents();
}

function bindActionMenuEvents() {
    els.fileActionRename?.addEventListener('click', () => {
        const id = Array.from(state.selectedJobIDs)[0] || Array.from(state.selectedFolderIDs)[0];
        const isFolder = state.selectedFolderIDs.size > 0;
        const name = prompt('새 이름을 입력하세요');
        if (name) {
            fetch(isFolder ? `/folders/${id}/rename` : `/job/${id}/rename`, {
                method: 'POST',
                body: new URLSearchParams({ new_name: name }),
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' }
            }).then(() => pollJobs(true));
        }
        els.fileMenu?.classList.remove('show');
    });

    els.fileActionDownload?.addEventListener('click', () => {
        window.submitDownload();
        els.fileMenu?.classList.remove('show');
    });
    els.fileActionDelete?.addEventListener('click', () => {
        window.confirmTrash();
        els.fileMenu?.classList.remove('show');
    });
    els.workActionNewFolder?.addEventListener('click', () => {
        window.createNewFolder();
        els.workMenu?.classList.remove('show');
    });
    els.workActionUpload?.addEventListener('click', () => {
        window.triggerFileUpload();
        els.workMenu?.classList.remove('show');
    });
    els.moveCancelBtn?.addEventListener('click', window.closeMoveDialog);
    els.moveConfirmBtn?.addEventListener('click', window.submitMove);
    els.moveUpBtn?.addEventListener('click', () => {
        const current = state.allFolders.find(folder => folder.ID === state.moveBrowseFolderID);
        state.moveBrowseFolderID = (current ? (current.ParentID || '') : '').trim();
        renderMoveBrowser();
    });
    els.cancelSelectionBtn?.addEventListener('click', clearSelection);
}
