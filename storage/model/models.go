package model

type Meeting struct {
	UUID     string
	Topic    string
	DateTime string
	Records  []Record
}

type Record struct {
	Id     string
	Type   string
	Status string
	Url    string
	Path   string
}
