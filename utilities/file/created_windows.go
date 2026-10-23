package file

import (
	"io/fs"
	"syscall"
	"time"
)

// createdAt is when the file was made, as NTFS keeps it.
func createdAt(stat fs.FileInfo) time.Time {
	if data, ok := stat.Sys().(*syscall.Win32FileAttributeData); ok && data != nil {
		return time.Unix(0, data.CreationTime.Nanoseconds())
	}
	return time.Time{}
}
