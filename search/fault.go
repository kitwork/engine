package search

type writerFaultPoint uint8

const (
	writerFaultBeforeManifest writerFaultPoint = iota + 1
	writerFaultAfterManifest
)

type writerFaultInjector func(writerFaultPoint)

func (writer *IndexWriter) injectFault(point writerFaultPoint) {
	if writer != nil && writer.fault != nil {
		writer.fault(point)
	}
}
