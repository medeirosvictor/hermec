package ui

// dialogMode says which modal dialog, if any, is open.
type dialogMode int

const (
	dlgNone dialogMode = iota
	dlgRename
	dlgLeave
)

// dialog is the open modal dialog; key is the saved-list key it is for.
type dialog struct {
	mode dialogMode
	key  string
}
