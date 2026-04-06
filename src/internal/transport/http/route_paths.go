package httptransport

// Common legacy route paths reused by HTML handlers and redirects.
const (
	rootPath      = "/"
	loginPath     = "/login"
	signupPath    = "/signup"
	logoutPath    = "/logout"
	filesPath     = "/files"
	filesHomePath = "/files/home"
	filesRootPath = "/files/root"
	trashPath     = "/trash"
	tagsPath      = "/tags"
	uploadPath    = "/upload"
)

// filesFolderPath builds the legacy folder URL used by the old files page.
func filesFolderPath(id string) string {
	return filesPath + "/folders/" + id
}

// jobPath builds the legacy job detail URL.
func jobPath(id string) string {
	return "/job/" + id
}
