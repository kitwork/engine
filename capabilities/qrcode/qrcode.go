package qrcode

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/utilities/napas"
	qr "github.com/kitwork/engine/utilities/qrcode"
	"github.com/kitwork/engine/value"
	skipqr "github.com/skip2/go-qrcode"
)

type QRCodeAdapter struct {
	scope   capabilities.Scope
	options qr.Options
}

func NewQRCodeAdapter(scope capabilities.Scope) *QRCodeAdapter {
	return &QRCodeAdapter{
		scope: scope,
		options: qr.Options{
			Cells: qr.Cells{
				Active: qr.Cell{
					Color: "#0f172a",
				},
			},
			Finders: qr.Finders{
				TopLeft: qr.Finder{
					Color: "#0f172a",
				},
				TopRight: qr.Finder{
					Color: "#0f172a",
				},
				BottomLeft: qr.Finder{
					Color: "#0f172a",
				},
			},
		},
	}
}

func (q *QRCodeAdapter) Data(v value.Value) *QRCodeAdapter {
	q.options.Data = parseDataInput(v)
	return q
}

func (q *QRCodeAdapter) Size(v value.Value) *QRCodeAdapter {
	if v.K == value.Number {
		q.options.Size = int(v.N)
	}
	return q
}

func (q *QRCodeAdapter) Logo(v value.Value) *QRCodeAdapter {
	if v.K == value.String {
		q.options.Logo.Image = v.Text()
	} else if v.K == value.Map {
		m := v.Map()
		if src, exists := m["src"]; exists {
			q.options.Logo.Image = src.Text()
		} else if img, exists := m["image"]; exists {
			q.options.Logo.Image = img.Text()
		}
		if size, exists := m["size"]; exists && size.K == value.Number {
			q.options.Logo.Size = size.N
		}
		if stroke, exists := m["stroke"]; exists {
			q.options.Logo.Stroke = stroke.Text()
		}
		if padding, exists := m["padding"]; exists && padding.K == value.Number {
			q.options.Logo.Padding = padding.N
		}
	}
	return q
}

func (q *QRCodeAdapter) Template(v value.Value) *QRCodeAdapter {
	str := strings.ToLower(v.Text())
	switch str {
	case "circular", "circle", "round":
		q.options.Template = "circular"
	default:
		q.options.Template = str
	}
	return q
}

func (q *QRCodeAdapter) Napas(v value.Value) *QRCodeAdapter {
	q.options.Data = parseDataInput(v)
	return q
}

func (q *QRCodeAdapter) Cell(v value.Value) *QRCodeAdapter {
	if v.K == value.String {
		q.options.Cells.Active.Color = v.Text()
	} else if v.K == value.Map {
		m := v.Map()
		if c, exists := m["color"]; exists {
			q.options.Cells.Active.Color = c.Text()
		} else if c, exists := m["colors"]; exists {
			var colors []string
			if c.K == value.Array {
				var arr []value.Value
				if p, ok := c.V.(*[]value.Value); ok && p != nil {
					arr = *p
				} else if s, ok := c.V.([]value.Value); ok {
					arr = s
				}
				colors = make([]string, len(arr))
				for i, v := range arr {
					colors[i] = v.Text()
				}
			} else if c.K == value.String {
				colors = strings.Split(c.Text(), ",")
			}
			if len(colors) > 0 {
				q.options.Cells.Active.Color = colors[0]
			}
		}
		if s, exists := m["size"]; exists && s.K == value.Number {
			q.options.Cells.Active.Size = s.N
		}
		if r, exists := m["rounded"]; exists && r.K == value.Number {
			q.options.Cells.Active.Rounded = r.N
		}
	}
	return q
}

func (q *QRCodeAdapter) Level(v value.Value) *QRCodeAdapter {
	str := strings.ToLower(v.Text())
	switch str {
	case "low", "l":
		q.options.Level = skipqr.Low
	case "medium", "meidum", "m":
		q.options.Level = skipqr.Medium
	case "high", "h":
		q.options.Level = skipqr.High
	case "highest", "q":
		q.options.Level = skipqr.Highest
	}
	return q
}

func (q *QRCodeAdapter) Alignment(v value.Value) *QRCodeAdapter {
	if v.K == value.String {
		color := v.Text()
		q.options.Alignment.Color = color
		q.options.Alignment.Stroke = color
	} else if v.K == value.Map {
		m := v.Map()
		if c, exists := m["color"]; exists {
			q.options.Alignment.Color = c.Text()
		}
		if s, exists := m["stroke"]; exists {
			q.options.Alignment.Stroke = s.Text()
		}
		if r, exists := m["rounded"]; exists {
			q.options.Alignment.Rounded = r.N
		}
		if t, exists := m["template"]; exists {
			q.options.Alignment.Template = t.Text()
		}
	}
	return q
}

func (q *QRCodeAdapter) Finder(args ...value.Value) *QRCodeAdapter {
	if len(args) == 0 {
		return q
	}

	var position string
	var config value.Value

	if len(args) == 1 {
		config = args[0]
		position = "all"
		if config.K == value.Map {
			m := config.Map()
			if posVal, exists := m["position"]; exists {
				position = strings.ToLower(posVal.Text())
			}
		}
	} else {
		position = strings.ToLower(args[0].Text())
		config = args[1]
	}

	colors, stroke, rounded, templateStr, ok := parseFinderConfig(config)
	if !ok {
		return q
	}

	apply := func(p string) {
		switch p {
		case "tl":
			if len(colors) > 0 {
				q.options.Finders.TopLeft.Color = colors[0]
			}
			q.options.Finders.TopLeft.Stroke = stroke
			if rounded != 0 {
				q.options.Finders.TopLeft.Rounded = rounded
			}
			if templateStr != "" {
				q.options.Finders.TopLeft.Template = templateStr
			}
		case "tr":
			if len(colors) > 0 {
				q.options.Finders.TopRight.Color = colors[0]
			}
			q.options.Finders.TopRight.Stroke = stroke
			if rounded != 0 {
				q.options.Finders.TopRight.Rounded = rounded
			}
			if templateStr != "" {
				q.options.Finders.TopRight.Template = templateStr
			}
		case "bl":
			if len(colors) > 0 {
				q.options.Finders.BottomLeft.Color = colors[0]
			}
			q.options.Finders.BottomLeft.Stroke = stroke
			if rounded != 0 {
				q.options.Finders.BottomLeft.Rounded = rounded
			}
			if templateStr != "" {
				q.options.Finders.BottomLeft.Template = templateStr
			}
		case "all", "":
			if len(colors) > 0 {
				q.options.Finders.TopLeft.Color = colors[0]
				q.options.Finders.TopRight.Color = colors[0]
				q.options.Finders.BottomLeft.Color = colors[0]
			}
			q.options.Finders.TopLeft.Stroke = stroke
			q.options.Finders.TopRight.Stroke = stroke
			q.options.Finders.BottomLeft.Stroke = stroke

			if templateStr != "" {
				q.options.Finders.TopLeft.Template = templateStr
				q.options.Finders.TopRight.Template = templateStr
				q.options.Finders.BottomLeft.Template = templateStr
			}

			if rounded != 0 {
				q.options.Finders.TopLeft.Rounded = rounded
				q.options.Finders.TopRight.Rounded = rounded
				q.options.Finders.BottomLeft.Rounded = rounded
			}
		}
	}

	apply(position)
	return q
}

func (q *QRCodeAdapter) AutoTheme() *QRCodeAdapter {
	q.options.Cells.Active.Color = "#0f172a"
	q.options.Finders.TopLeft.Color = "#0f172a"
	q.options.Finders.TopRight.Color = "#0f172a"
	q.options.Finders.BottomLeft.Color = "#0f172a"
	return q
}

func (q *QRCodeAdapter) Generate(v value.Value, sizeVal ...value.Value) value.Value {
	q.options.Data = parseDataInput(v)
	if len(sizeVal) > 0 && sizeVal[0].K == value.Number {
		q.options.Size = int(sizeVal[0].N)
	}
	return q.Svg()
}

func (q *QRCodeAdapter) Svg() value.Value {
	svgStr, err := q.options.Svg()
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	return value.New(svgStr)
}

func (q *QRCodeAdapter) Png() value.Value {
	pngBytes, err := q.options.Png()
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	return value.New(pngBytes)
}

func (q *QRCodeAdapter) Base64() value.Value {
	pngBytes, err := q.options.Png()
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	encoded := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	return value.New(encoded)
}

func parseDataInput(v value.Value) string {
	if v.K == value.Map {
		m := v.Map()

		// Case 1: VietQR / Napas payload mapping
		bankIDVal, hasBank := m["bank"]
		if !hasBank {
			bankIDVal, hasBank = m["bin"]
		}
		if !hasBank {
			bankIDVal, hasBank = m["bank_id"]
		}
		accVal, hasAcc := m["account"]
		if !hasAcc {
			accVal, hasAcc = m["account_number"]
		}
		if !hasAcc {
			accVal, hasAcc = m["acc"]
		}

		if hasBank && hasAcc {
			amount := float64(0)
			if amtVal, ok := m["amount"]; ok {
				if amtVal.K == value.Number {
					amount = amtVal.N
				} else if amtVal.K == value.String {
					amount, _ = strconv.ParseFloat(amtVal.Text(), 64)
				}
			}
			memo := ""
			if memoVal, ok := m["memo"]; ok {
				memo = memoVal.Text()
			} else if memoVal, ok := m["addInfo"]; ok {
				memo = memoVal.Text()
			} else if memoVal, ok := m["description"]; ok {
				memo = memoVal.Text()
			}

			n := napas.New().Bank(bankIDVal.Text(), accVal.Text())
			if amount > 0 {
				n.Amount(fmt.Sprintf("%.0f", amount))
			}
			if memo != "" {
				n.Info(memo)
			}
			if payload, err := n.Generate(); err == nil && payload != "" {
				return payload
			}
		}

		// Case 2: WiFi config mapping
		var ssid string
		if val, ok := m["ssid"]; ok {
			ssid = val.Text()
		} else if val, ok := m["wifi"]; ok {
			ssid = val.Text()
		}
		if ssid != "" {
			pass := ""
			if val, ok := m["password"]; ok {
				pass = val.Text()
			} else if val, ok := m["pass"]; ok {
				pass = val.Text()
			}
			enc := "WPA"
			if val, ok := m["encryption"]; ok {
				enc = strings.ToUpper(val.Text())
			} else if val, ok := m["type"]; ok {
				enc = strings.ToUpper(val.Text())
			}
			return fmt.Sprintf("WIFI:T:%s;S:%s;P:%s;;", enc, ssid, pass)
		}

		// Case 3: Email mapping
		var email string
		if val, ok := m["email"]; ok {
			email = val.Text()
		} else if val, ok := m["mail"]; ok {
			email = val.Text()
		}
		if email != "" {
			subject := ""
			if val, ok := m["subject"]; ok {
				subject = val.Text()
			}
			body := ""
			if val, ok := m["body"]; ok {
				body = val.Text()
			}
			escape := url.QueryEscape
			res := "mailto:" + email
			hasParams := false
			if subject != "" {
				res += "?subject=" + escape(subject)
				hasParams = true
			}
			if body != "" {
				if hasParams {
					res += "&body=" + escape(body)
				} else {
					res += "?body=" + escape(body)
				}
			}
			return res
		}

		// Case 4: SMS mapping
		var smsPhone string
		for _, k := range []string{"sms_to", "phone", "sms", "telephone", "mobile"} {
			if val, ok := m[k]; ok {
				smsPhone = val.Text()
				break
			}
		}
		var smsBody string
		for _, k := range []string{"message", "body", "sms_body", "content"} {
			if val, ok := m[k]; ok {
				smsBody = val.Text()
				break
			}
		}
		if smsPhone != "" && smsBody != "" {
			return fmt.Sprintf("SMSTO:%s:%s", smsPhone, smsBody)
		}

		// Case 5: Phone mapping (without body)
		if smsPhone != "" {
			return "tel:" + smsPhone
		}

		// Case 6: Geolocation mapping
		var lat, lon string
		if val, ok := m["lat"]; ok {
			lat = val.Text()
		} else if val, ok := m["latitude"]; ok {
			lat = val.Text()
		}
		if val, ok := m["lon"]; ok {
			lon = val.Text()
		} else if val, ok := m["lng"]; ok {
			lon = val.Text()
		} else if val, ok := m["longitude"]; ok {
			lon = val.Text()
		} else if val, ok := m["long"]; ok {
			lon = val.Text()
		}
		if lat != "" && lon != "" {
			return fmt.Sprintf("geo:%s,%s", lat, lon)
		}

		// Case 7: Text/URL fallback extraction
		for _, k := range []string{"url", "link", "href", "text", "content", "data", "message", "val", "value"} {
			if val, ok := m[k]; ok {
				return val.Text()
			}
		}
	}
	return v.Text()
}

func parseFinderConfig(configVal value.Value) (colors []string, stroke string, rounded float64, template string, ok bool) {
	if configVal.K == value.String {
		colors = []string{configVal.Text()}
		stroke = configVal.Text()
		return colors, stroke, 0, "", true
	}
	if configVal.K != value.Map {
		return nil, "", 0, "", false
	}
	m := configVal.Map()
	if t, exists := m["template"]; exists {
		template = t.Text()
	}

	if c, exists := m["color"]; exists {
		colors = []string{c.Text()}
	} else if c, exists := m["colors"]; exists {
		if c.K == value.Array {
			var arr []value.Value
			if p, ok := c.V.(*[]value.Value); ok && p != nil {
				arr = *p
			} else if s, ok := c.V.([]value.Value); ok {
				arr = s
			}
			colors = make([]string, len(arr))
			for i, v := range arr {
				colors[i] = v.Text()
			}
		} else if c.K == value.String {
			colors = strings.Split(c.Text(), ",")
		}
	}
	if s, exists := m["stroke"]; exists {
		stroke = s.Text()
	}
	if r, exists := m["rounded"]; exists && r.K == value.Number {
		rounded = r.N
	}

	return colors, stroke, rounded, template, true
}

func Register(registry *capabilities.Registry) {
	registry.Register("qrcode", func(scope capabilities.Scope) value.Value {
		return value.New(NewQRCodeAdapter(scope))
	})
}

func init() {
	Register(capabilities.DefaultRegistry)
}
