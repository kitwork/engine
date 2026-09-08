package javascript

import "bytes"

const (
	nativeHostRuntimeFragment = "src/native-host.js"
	serviceRuntimeFragment    = "src/service.js"
)

func standaloneServiceRuntimeSource() ([]byte, error) {
	return exactFragmentSource([]string{serviceRuntimeFragment})
}

func stagedServiceRuntimeSource() ([]byte, error) {
	return exactFragmentSource([]string{serviceRuntimeFragment})
}

func appendNativeHostRuntime(source []byte) ([]byte, error) {
	nativeHost, err := exactFragmentSource([]string{nativeHostRuntimeFragment})
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	output.Grow(len(source) + len(nativeHost))
	_, _ = output.Write(source)
	_, _ = output.Write(nativeHost)
	return output.Bytes(), nil
}
