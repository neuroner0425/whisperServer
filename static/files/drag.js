function bindDragAndDropHandlers() {
    document.querySelectorAll('#entries-grid [draggable="true"]').forEach(card => {
        card.ondragstart = event => {
            if (!card.classList.contains('is-selected')) {
                clearSelection();
                setCardSelected(card, true);
                syncUI();
            }
            event.dataTransfer.setData('application/json', JSON.stringify({
                job_ids: Array.from(state.selectedJobIDs),
                folder_ids: Array.from(state.selectedFolderIDs)
            }));
            event.dataTransfer.effectAllowed = 'move';
        };
    });

    document.querySelectorAll('.folder-drop-target, .crumb-drop-target').forEach(target => {
        target.ondragover = event => {
            event.preventDefault();
            target.classList.add('drop-highlight');
        };
        target.ondragleave = () => target.classList.remove('drop-highlight');
        target.ondrop = event => {
            event.preventDefault();
            target.classList.remove('drop-highlight');
            try {
                const payload = JSON.parse(event.dataTransfer.getData('application/json'));
                const targetID = (target.dataset.folderId || '').trim();
                if (payload.folder_ids.includes(targetID)) return;
                const formData = new URLSearchParams({ target_folder_id: targetID });
                payload.job_ids.forEach(id => formData.append('job_ids', id));
                payload.folder_ids.forEach(id => formData.append('folder_ids', id));
                fetch('/batch-move', { method: 'POST', body: formData, headers: { 'Content-Type': 'application/x-www-form-urlencoded' } })
                    .then(() => pollJobs(true));
            } catch (_) {}
        };
    });
}

function initDragBox() {
    let startX;
    let startY;
    document.addEventListener('mousedown', event => {
        if (event.button !== 0 || !isFormEmptyArea(event.target)) return;
        const rect = els.form.getBoundingClientRect();
        startX = event.clientX - rect.left + els.form.scrollLeft;
        startY = event.clientY - rect.top + els.form.scrollTop;
        state.isDraggingBox = true;
        state.dragMoved = false;
        if (!event.ctrlKey && !event.metaKey && !event.shiftKey) clearSelection();
        els.selBox.style.width = '0';
        els.selBox.style.height = '0';
        els.selBox.style.left = `${startX}px`;
        els.selBox.style.top = `${startY}px`;
        els.selBox.style.display = 'block';
        document.body.classList.add('is-selecting');
    });

    document.addEventListener('mousemove', event => {
        if (!state.isDraggingBox) return;
        const rect = els.form.getBoundingClientRect();
        const currentX = Math.max(0, Math.min(event.clientX - rect.left + els.form.scrollLeft, els.form.scrollWidth));
        const currentY = Math.max(0, Math.min(event.clientY - rect.top + els.form.scrollTop, els.form.scrollHeight));
        const left = Math.min(startX, currentX);
        const top = Math.min(startY, currentY);
        const width = Math.abs(startX - currentX);
        const height = Math.abs(startY - currentY);
        els.selBox.style.left = `${left}px`;
        els.selBox.style.top = `${top}px`;
        els.selBox.style.width = `${width}px`;
        els.selBox.style.height = `${height}px`;
        if (width <= 5 && height <= 5) return;

        state.dragMoved = true;
        const box = {
            left: left + rect.left - els.form.scrollLeft,
            top: top + rect.top - els.form.scrollTop,
            right: left + width + rect.left - els.form.scrollLeft,
            bottom: top + height + rect.top - els.form.scrollTop
        };
        document.querySelectorAll('.entry-card, .entry-row').forEach(item => {
            const itemRect = item.getBoundingClientRect();
            if (!(itemRect.right < box.left || itemRect.left > box.right || itemRect.bottom < box.top || itemRect.top > box.bottom)) {
                setCardSelected(item, true);
            }
        });
        syncUI();
    });

    document.addEventListener('mouseup', () => {
        if (!state.isDraggingBox) return;
        state.isDraggingBox = false;
        els.selBox.style.display = 'none';
        document.body.classList.remove('is-selecting');
        state.justFinishedDragging = true;
        setTimeout(() => { state.justFinishedDragging = false; }, 100);
    });
}
