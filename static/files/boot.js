window.initApp = function (config) {
    initElements();
    Object.assign(state, config);
    if (config.allFolders) refreshAllFolders(config.allFolders);
    bindGlobalEvents();
    bindFilterEvents();
    initDragBox();
    pollJobs(true);
    setInterval(() => pollJobs(false), 3000);
};
