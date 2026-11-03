package localfiles

// SetMaxDocXML sets the docx markup limit for a test and returns the restore.
func SetMaxDocXML(n int64) func() {
	old := maxDocXMLBytes
	maxDocXMLBytes = n
	return func() { maxDocXMLBytes = old }
}
