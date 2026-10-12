//go:build kitdbfault

package checkpointfault

import (
	"errors"
	"io"
	"os"
	"syscall"
)

func ioSelected(path, point string) bool {
	return os.Getenv("KITDB_IO_FAULT_PATH") == path && os.Getenv("KITDB_IO_FAULT_POINT") == point
}

func recordIO(path, point string) error {
	file, err := os.OpenFile(path+".io-reached", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.WriteString(point + "\n")
	return errors.Join(err, file.Close())
}

func Error(path, point string) error {
	if !ioSelected(path, point) {
		return nil
	}
	if err := recordIO(path, point); err != nil {
		return err
	}
	return &os.PathError{Op: point, Path: path, Err: syscall.EIO}
}

func WriteError(path, point string, file io.Writer, data []byte, offset int64) error {
	if !ioSelected(path, point) {
		return nil
	}
	if err := recordIO(path, point); err != nil {
		return err
	}
	if os.Getenv("KITDB_IO_FAULT_KIND") == "space" {
		return &os.PathError{Op: point, Path: path, Err: syscall.ENOSPC}
	}
	// Write an actual incomplete prefix, then report failure. The rest must
	// never be written by the normal write-all loop after this error.
	var err error
	if offset < 0 {
		_, err = file.Write(data[:len(data)/2])
	} else {
		at, ok := file.(io.WriterAt)
		if !ok {
			return io.ErrShortWrite
		}
		_, err = at.WriteAt(data[:len(data)/2], offset)
	}
	if err != nil {
		return err
	}
	return &os.PathError{Op: point, Path: path, Err: io.ErrShortWrite}
}
