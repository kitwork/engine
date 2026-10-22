package kitdb

func latencyPageRow(page *rowPage, index int) mainRow { return page.row(index) }
func latencyPageKey(page *rowPage, index int) []byte  { return page.key(index) }
