package routes

const (
	Root      = "/"
	Login     = "/login"
	Signup    = "/signup"
	Logout    = "/logout"
	Files     = "/files"
	FilesHome = "/files/home"
	FilesRoot = "/files/root"
	Trash     = "/trash"
	Tags      = "/tags"
	Upload    = "/upload"
)

func FilesFolder(id string) string {
	return Files + "/folders/" + id
}

func Job(id string) string {
	return "/job/" + id
}
