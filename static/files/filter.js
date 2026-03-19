function bindFilterEvents() {
    if (!els.sortFilterToggle || !els.sortFilterPanel) return;

    els.sortFilterToggle.onclick = event => {
        event.stopPropagation();
        els.sortFilterPanel.classList.toggle('show');
    };

    document.addEventListener('click', event => {
        if (!els.sortFilterPanel.contains(event.target) && event.target !== els.sortFilterToggle) {
            els.sortFilterPanel.classList.remove('show');
        }
    });

    const updateFilterUI = () => {
        const sort = els.searchSort?.value || 'updated';
        const order = els.searchOrder?.value || 'desc';
        document.querySelectorAll('.filter-option').forEach(option => {
            const kind = option.dataset.kind;
            const value = option.dataset.value;
            option.classList.toggle('active', (kind === 'sort' && value === sort) || (kind === 'order' && value === order));
        });
        const sortLabel = sort === 'name' ? '이름' : '수정 날짜';
        const orderLabel = order === 'asc' ? '오름차순' : '내림차순';
        const labelEl = document.getElementById('sort-filter-label');
        if (labelEl) labelEl.textContent = `${sortLabel} · ${orderLabel}`;
    };

    document.querySelectorAll('.filter-option').forEach(option => {
        option.onclick = () => {
            const kind = option.dataset.kind;
            const value = option.dataset.value;
            if (kind === 'sort' && els.searchSort) els.searchSort.value = value;
            if (kind === 'order' && els.searchOrder) els.searchOrder.value = value;
            updateFilterUI();
            pollJobs(true);
        };
    });
    updateFilterUI();
}
