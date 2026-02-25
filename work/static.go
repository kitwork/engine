package work

type Folder struct {
	router *Router
	method string
	path   string
	folder string
}

type File struct {
	router *Router
	method string
	path   string
	file   string
}
