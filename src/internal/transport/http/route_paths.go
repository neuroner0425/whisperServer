package httptransport

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

func filesFolderPath(id string) string {
	return filesPath + "/folders/" + id
}

func jobPath(id string) string {
	return "/job/" + id
}
