package qrcode

import (
	"github.com/kitwork/engine/capabilities"
	qr "github.com/kitwork/engine/utilities/qrcode"
	"github.com/kitwork/engine/value"
	goqrcode "github.com/skip2/go-qrcode"
)

type QRCodeAdapter struct {
	scope   capabilities.Scope
	options qr.Options
}

func NewQRCodeAdapter(scope capabilities.Scope) *QRCodeAdapter {
	return &QRCodeAdapter{scope: scope}
}

func (q *QRCodeAdapter) Data(dataVal value.Value) *QRCodeAdapter {
	q.options.Data = dataVal.Text()
	return q
}

func (q *QRCodeAdapter) Size(sizeVal value.Value) *QRCodeAdapter {
	if sizeVal.K == value.Number {
		q.options.Size = int(sizeVal.N)
	}
	return q
}

func (q *QRCodeAdapter) Generate(content value.Value, size value.Value) value.Value {
	q.Data(content)
	if size.K == value.Number {
		q.Size(size)
	}
	return q.Png()
}

func (q *QRCodeAdapter) Png() value.Value {
	if q.options.Data == "" {
		return value.Value{K: value.Invalid, V: "qrcode: data is required"}
	}
	size := q.options.Size
	if size <= 0 {
		size = 256
	}
	pngBytes, err := goqrcode.Encode(q.options.Data, goqrcode.Medium, size)
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	return value.New(pngBytes)
}

func init() {
	capabilities.DefaultRegistry.Register("qrcode", func(scope capabilities.Scope) value.Value {
		return value.New(NewQRCodeAdapter(scope))
	})
}
