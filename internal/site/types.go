package site

type Page struct {
	Slug       string
	Title      string
	Summary    string
	Tags       []string
	Categories []string
	Date       string
	URL        string
	Lang       string
	RawHTML    string
}
