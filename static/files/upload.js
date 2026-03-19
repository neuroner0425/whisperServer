function bindUploadEvents() {
    if (els.fileInput) {
        els.fileInput.onchange = () => {
            const file = els.fileInput.files[0];
            if (!file || !els.uploadModal) return;
            if (els.uploadFileDisplayName) els.uploadFileDisplayName.textContent = file.name;
            if (els.uploadFilename) {
                const dotIndex = file.name.lastIndexOf('.');
                els.uploadFilename.value = dotIndex > 0 ? file.name.slice(0, dotIndex) : file.name;
            }
            if (els.uploadFolderID) els.uploadFolderID.value = state.currentFolderID || '';
            if (els.uploadTag) els.uploadTag.value = '';
            if (els.uploadDesc) els.uploadDesc.value = '';
            els.uploadModal.classList.add('show');
        };
    }
    if (!els.uploadForm) return;
    els.uploadForm.onsubmit = event => {
        event.preventDefault();
        const formData = new FormData(els.uploadForm);
        const file = els.fileInput.files[0];
        if (!file) return;
        const uploadID = `up-${Date.now()}`;
        state.pendingUploads[uploadID] = {
            id: uploadID,
            filename: formData.get('display_name') || file.name,
            progress: 0,
            folderID: formData.get('folder_id')
        };
        window.closeUploadModal();
        renderEntries();

        const xhr = new XMLHttpRequest();
        xhr.open('POST', '/upload');
        xhr.upload.onprogress = progressEvent => {
            if (!progressEvent.lengthComputable) return;
            state.pendingUploads[uploadID].progress = Math.round((progressEvent.loaded / progressEvent.total) * 100);
            renderEntries();
        };
        xhr.onload = () => {
            delete state.pendingUploads[uploadID];
            pollJobs(true);
        };
        xhr.send(formData);
    };
}
