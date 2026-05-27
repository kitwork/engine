package work

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kitwork/engine/utilities/napas"
	qr "github.com/kitwork/engine/utilities/qrcode"
	"github.com/kitwork/engine/value"
	"github.com/skip2/go-qrcode"
)

// ==========================================
// Kitwork Engine Wrapper methods & structs
// ==========================================

func (w *KitWork) Qrcode() *Qrcode {
	q := &Qrcode{
		tenant: w.tenant,
	}
	q.options.Template = "square"
	q.options.Padding = 2
	q.options.Level = qrcode.Medium
	q.options.Size = 256
	q.options.Cells.Active = qr.Cell{
		Color:   "#000000",
		Size:    0.85,
		Rounded: 0,
		Opacity: 1.0,
	}
	q.options.Center = qr.Center{
		Logo:       "",
		Background: "#ffffff",
		Shape:      "square",
		Size:       0.2,
		Padding:    0.2,
	}
	q.options.Finders.TopLeft = qr.Finder{
		Color:   "#000000",
		Stroke:  "#000000",
		Rounded: 2.0,
	}
	q.options.Finders.TopRight = qr.Finder{
		Color:   "#000000",
		Stroke:  "#000000",
		Rounded: 2.0,
	}
	q.options.Finders.BottomLeft = qr.Finder{
		Color:   "#000000",
		Stroke:  "#000000",
		Rounded: 2.0,
	}
	q.options.Background = qr.Background{
		Color: "#ffffff",
	}
	q.options.Merge = true
	return q
}

type Qrcode struct {
	tenant  *Tenant
	options qr.Options
}

// Generate provides backward compatibility for drawing simple QR codes
func (q *Qrcode) Generate(content value.Value, size value.Value) value.Value {
	q.Data(content)
	if size.K == value.Number {
		q.Size(size)
	}
	return q.Png()
}

func (q *Qrcode) Data(dataVal value.Value) *Qrcode {
	q.options.Data = parseDataInput(dataVal)
	return q
}

func (q *Qrcode) Template(v value.Value) *Qrcode {
	q.options.Template = v.Text()
	switch q.options.Template {
	case "dot", "circle", "circular":
		q.options.Cells.Active.Size = 0.75
		q.options.Finders.TopLeft.Rounded = 3.5
		q.options.Finders.TopRight.Rounded = 3.5
		q.options.Finders.BottomLeft.Rounded = 3.5
	case "rounded":
		q.options.Cells.Active.Size = 0.85
		q.options.Finders.TopLeft.Rounded = 1.5
		q.options.Finders.TopRight.Rounded = 1.5
		q.options.Finders.BottomLeft.Rounded = 1.5
	case "square":
		q.options.Cells.Active.Size = 1.0
		q.options.Finders.TopLeft.Rounded = 0.0
		q.options.Finders.TopRight.Rounded = 0.0
		q.options.Finders.BottomLeft.Rounded = 0.0
	}
	return q
}

func (q *Qrcode) Logo(v value.Value) *Qrcode {
	q.options.Center.Logo = v.Text()
	return q
}

func (q *Qrcode) Center(v value.Value) *Qrcode {
	if v.K == value.Map {
		m := v.Map()
		if l, exists := m["logo"]; exists {
			q.options.Center.Logo = l.Text()
		}
		if bg, exists := m["background"]; exists {
			q.options.Center.Background = bg.Text()
		}
		if s, exists := m["shape"]; exists {
			q.options.Center.Shape = s.Text()
		}
		if sz, exists := m["size"]; exists && sz.K == value.Number {
			q.options.Center.Size = sz.N
		}
		if pad, exists := m["padding"]; exists && pad.K == value.Number {
			q.options.Center.Padding = pad.N
		}
	}
	return q
}

func (q *Qrcode) CellColor(v value.Value) *Qrcode {
	q.options.Cells.Active.Color = v.Text()
	return q
}

func (q *Qrcode) CellSize(v value.Value) *Qrcode {
	if v.K == value.Number {
		q.options.Cells.Active.Size = v.N
	}
	return q
}

func (q *Qrcode) BorderColor(v value.Value) *Qrcode {
	q.options.Background.Stroke = v.Text()
	return q
}

func (q *Qrcode) BorderSize(v value.Value) *Qrcode {
	if v.K == value.Number {
		q.options.Background.Border = v.N
	}
	return q
}

func (q *Qrcode) Padding(v value.Value) *Qrcode {
	if v.K == value.Number {
		q.options.Padding = int(v.N)
	}
	return q
}

func (q *Qrcode) Size(v value.Value) *Qrcode {
	if v.K == value.Number {
		q.options.Size = int(v.N)
	}
	return q
}

func (q *Qrcode) Merge(v value.Value) *Qrcode {
	q.options.Merge = v.Truthy()
	return q
}

func (q *Qrcode) CellGradient(typeVal value.Value, colorsVal value.Value, angleVal value.Value) *Qrcode {
	grad := &qr.Gradient{
		Type: typeVal.Text(),
	}
	if colorsVal.K == value.Array {
		var arr []value.Value
		if p, ok := colorsVal.V.(*[]value.Value); ok && p != nil {
			arr = *p
		} else if s, ok := colorsVal.V.([]value.Value); ok {
			arr = s
		}
		grad.Colors = make([]string, len(arr))
		for i, v := range arr {
			grad.Colors[i] = v.Text()
		}
	} else if colorsVal.K == value.String {
		grad.Colors = strings.Split(colorsVal.Text(), ",")
	}

	if angleVal.K == value.Number {
		grad.Angle = angleVal.N
	} else {
		grad.Angle = 45.0
	}
	q.options.Cells.Active.Gradient = grad
	return q
}

// --- General Finder Setters ---

func (q *Qrcode) FinderColor(v value.Value) *Qrcode {
	colorStr := v.Text()
	q.options.Finders.TopLeft.Color = colorStr
	q.options.Finders.TopRight.Color = colorStr
	q.options.Finders.BottomLeft.Color = colorStr
	return q
}

func (q *Qrcode) FinderStroke(v value.Value) *Qrcode {
	strokeStr := v.Text()
	q.options.Finders.TopLeft.Stroke = strokeStr
	q.options.Finders.TopRight.Stroke = strokeStr
	q.options.Finders.BottomLeft.Stroke = strokeStr
	return q
}

func (q *Qrcode) FinderRounded(v value.Value) *Qrcode {
	if v.K == value.Number {
		q.options.Finders.TopLeft.Rounded = v.N
		q.options.Finders.TopRight.Rounded = v.N
		q.options.Finders.BottomLeft.Rounded = v.N
	}
	return q
}

func (q *Qrcode) FinderGradient(typeVal value.Value, colorsVal value.Value, angleVal value.Value) *Qrcode {
	gradType := typeVal.Text()
	var gradColors []string
	if colorsVal.K == value.Array {
		var arr []value.Value
		if p, ok := colorsVal.V.(*[]value.Value); ok && p != nil {
			arr = *p
		} else if s, ok := colorsVal.V.([]value.Value); ok {
			arr = s
		}
		gradColors = make([]string, len(arr))
		for i, v := range arr {
			gradColors[i] = v.Text()
		}
	} else if colorsVal.K == value.String {
		gradColors = strings.Split(colorsVal.Text(), ",")
	}

	var angle float64
	if angleVal.K == value.Number {
		angle = angleVal.N
	} else {
		angle = 45.0
	}

	grad := &qr.Gradient{
		Type:   gradType,
		Colors: gradColors,
		Angle:  angle,
	}

	q.options.Finders.TopLeft.Gradient = grad
	q.options.Finders.TopRight.Gradient = grad
	q.options.Finders.BottomLeft.Gradient = grad
	return q
}

// --- Smart Unified APIs ---

func parseDataInput(v value.Value) string {
	if v.K == value.Struct {
		if n, ok := v.V.(*Napas); ok && n != nil {
			return n.Payload()
		}
	}
	if v.K == value.Map {
		m := v.Map()

		// Nested objects check: { napas: ... }, { vietqr: ... }, { wifi: ... }, { contact: ... }, { vcard: ... }
		if nestedVal, exists := m["napas"]; exists && nestedVal.K == value.Map {
			return parseDataInput(nestedVal)
		}
		if nestedVal, exists := m["vietqr"]; exists && nestedVal.K == value.Map {
			return parseDataInput(nestedVal)
		}
		if nestedVal, exists := m["wifi"]; exists && nestedVal.K == value.Map {
			return parseDataInput(nestedVal)
		}
		if nestedVal, exists := m["contact"]; exists && nestedVal.K == value.Map {
			return parseDataInput(nestedVal)
		}
		if nestedVal, exists := m["vcard"]; exists && nestedVal.K == value.Map {
			return parseDataInput(nestedVal)
		}

		// Case 1: Napas/VietQR configuration map
		var bin, account string
		for _, k := range []string{"bin", "bank", "napas", "bank_bin", "bank_code"} {
			if val, ok := m[k]; ok {
				bin = val.Text()
				break
			}
		}
		for _, k := range []string{"account_number", "account", "number", "acc", "account_no", "stk", "stk_napas"} {
			if val, ok := m[k]; ok {
				account = val.Text()
				break
			}
		}

		isNapas := (bin != "" && account != "") || (m["type"].Text() == "napas" || m["type"].Text() == "vietqr")

		if isNapas {
			n := &Napas{
				core: &napas.Napas{
					CountryCode:   "VN",
					Method:        "11",
					ServiceCode:   "QRIBFTTA",
					Bin:           bin,
					AccountNumber: account,
				},
			}

			// Extract amount
			for _, k := range []string{"amount", "money", "val", "value", "so_tien"} {
				if val, ok := m[k]; ok {
					switch val.K {
					case value.String:
						n.core.AmountVal = val.Text()
					case value.Number:
						vN := val.N
						if vN == float64(int64(vN)) {
							n.core.AmountVal = strconv.FormatInt(int64(vN), 10)
						} else {
							n.core.AmountVal = strconv.FormatFloat(vN, 'f', -1, 64)
						}
					}
					break
				}
			}

			// Extract info
			for _, k := range []string{"info", "add_info", "message", "msg", "description", "desc", "content", "noi_dung"} {
				if val, ok := m[k]; ok {
					n.core.AddInfo = val.Text()
					break
				}
			}

			// Extract receiver
			for _, k := range []string{"receiver", "merchant_name", "name", "receiver_name", "ten_nguoi_nhan"} {
				if val, ok := m[k]; ok {
					n.core.MerchantName = val.Text()
					break
				}
			}

			// Extract city
			for _, k := range []string{"city", "merchant_city"} {
				if val, ok := m[k]; ok {
					n.core.MerchantCity = val.Text()
					break
				}
			}

			if methodVal, exists := m["method"]; exists {
				n.core.Method = methodVal.Text()
			}
			for _, k := range []string{"service", "service_code"} {
				if val, ok := m[k]; ok {
					n.core.ServiceCode = val.Text()
					break
				}
			}
			return n.core.Payload()
		}

		// Case 8: vCard contact card
		{
			var name, phone, emailVal, company, title, url string
			for _, k := range []string{"name", "full_name", "first_name", "contact_name", "ho_ten", "ten"} {
				if val, ok := m[k]; ok {
					name = val.Text()
					break
				}
			}
			for _, k := range []string{"phone", "mobile", "telephone", "tel", "sdt", "so_dien_thoai"} {
				if val, ok := m[k]; ok {
					phone = val.Text()
					break
				}
			}
			for _, k := range []string{"email", "mail", "email_address"} {
				if val, ok := m[k]; ok {
					emailVal = val.Text()
					break
				}
			}
			for _, k := range []string{"company", "organization", "org", "cong_ty"} {
				if val, ok := m[k]; ok {
					company = val.Text()
					break
				}
			}
			for _, k := range []string{"title", "job_title", "role", "chuc_vu"} {
				if val, ok := m[k]; ok {
					title = val.Text()
					break
				}
			}
			for _, k := range []string{"url", "website", "web", "link"} {
				if val, ok := m[k]; ok {
					url = val.Text()
					break
				}
			}

			isContact := m["type"].Text() == "contact" || m["type"].Text() == "vcard" || (name != "" && (phone != "" || emailVal != ""))

			if isContact {
				var sb strings.Builder
				sb.WriteString("BEGIN:VCARD\n")
				sb.WriteString("VERSION:3.0\n")
				if name != "" {
					sb.WriteString(fmt.Sprintf("FN:%s\n", name))
					sb.WriteString(fmt.Sprintf("N:%s;;;;\n", name))
				}
				if phone != "" {
					sb.WriteString(fmt.Sprintf("TEL;TYPE=CELL:%s\n", phone))
				}
				if emailVal != "" {
					sb.WriteString(fmt.Sprintf("EMAIL;TYPE=INTERNET:%s\n", emailVal))
				}
				if company != "" {
					sb.WriteString(fmt.Sprintf("ORG:%s\n", company))
				}
				if title != "" {
					sb.WriteString(fmt.Sprintf("TITLE:%s\n", title))
				}
				if url != "" {
					sb.WriteString(fmt.Sprintf("URL:%s\n", url))
				}
				sb.WriteString("END:VCARD")
				return sb.String()
			}
		}

		// Case 2: WiFi configuration map
		var ssid string
		for _, k := range []string{"ssid", "name", "wifi_name", "wifi"} {
			if val, ok := m[k]; ok {
				if k == "wifi" && val.K == value.Map {
					continue
				}
				ssid = val.Text()
				break
			}
		}

		if ssid != "" || m["type"].Text() == "wifi" {
			password := ""
			for _, k := range []string{"password", "pass", "key", "pin", "pwd"} {
				if val, ok := m[k]; ok {
					password = val.Text()
					break
				}
			}
			encryption := "WPA"
			for _, k := range []string{"encryption", "type", "auth", "sec", "security"} {
				if val, ok := m[k]; ok {
					if val.Text() != "wifi" {
						encryption = val.Text()
						break
					}
				}
			}
			if password == "" {
				encryption = "nopass"
			}
			hidden := "false"
			if h, exists := m["hidden"]; exists {
				if h.K == value.Bool {
					if h.N == 1 {
						hidden = "true"
					}
				} else if h.Text() == "true" {
					hidden = "true"
				}
			}
			return fmt.Sprintf("WIFI:S:%s;T:%s;P:%s;H:%s;;", ssid, encryption, password, hidden)
		}

		// Case 3: Email / Mail mapping
		var email string
		for _, k := range []string{"email", "mail", "to", "email_to"} {
			if val, ok := m[k]; ok {
				if strings.Contains(val.Text(), "@") {
					email = val.Text()
					break
				}
			}
		}
		if email != "" || m["type"].Text() == "email" || m["type"].Text() == "mail" {
			if email == "" {
				if val, ok := m["email"]; ok {
					email = val.Text()
				} else if val, ok := m["to"]; ok {
					email = val.Text()
				}
			}
			subject := ""
			for _, k := range []string{"subject", "title"} {
				if val, ok := m[k]; ok {
					subject = val.Text()
					break
				}
			}
			body := ""
			for _, k := range []string{"body", "message", "msg", "content"} {
				if val, ok := m[k]; ok {
					body = val.Text()
					break
				}
			}

			escape := func(s string) string {
				s = strings.ReplaceAll(s, "%", "%25")
				s = strings.ReplaceAll(s, " ", "%20")
				s = strings.ReplaceAll(s, "?", "%3F")
				s = strings.ReplaceAll(s, "&", "%26")
				s = strings.ReplaceAll(s, "=", "%3D")
				return s
			}
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

func (q *Qrcode) Napas(v value.Value) *Qrcode {
	q.Data(value.New(""))
	q.options.Data = parseDataInput(v)
	return q
}

func (q *Qrcode) Cell(v value.Value) *Qrcode {
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
		if grad, exists := m["gradient"]; exists {
			if grad.K == value.Map {
				gm := grad.Map()
				g := &qr.Gradient{}
				if gt, exists := gm["type"]; exists {
					g.Type = gt.Text()
				}
				if gc, exists := gm["colors"]; exists {
					var gradColors []string
					if gc.K == value.Array {
						var arr []value.Value
						if p, ok := gc.V.(*[]value.Value); ok && p != nil {
							arr = *p
						} else if s, ok := gc.V.([]value.Value); ok {
							arr = s
						}
						gradColors = make([]string, len(arr))
						for i, v := range arr {
							gradColors[i] = v.Text()
						}
					} else if gc.K == value.String {
						gradColors = strings.Split(gc.Text(), ",")
					}
					if len(gradColors) > 0 {
						g.Colors = gradColors
					}
				}
				if ga, exists := gm["angle"]; exists && ga.K == value.Number {
					g.Angle = ga.N
				} else {
					g.Angle = 45.0
				}
				q.options.Cells.Active.Gradient = g
			}
		}
	}
	return q
}

func parseFinderConfig(configVal value.Value) (colors []string, stroke string, rounded float64, gradType string, gradAngle float64, ok bool) {
	if configVal.K == value.String {
		colors = []string{configVal.Text()}
		stroke = configVal.Text()
		return colors, stroke, 0, "", 0, true
	}
	if configVal.K != value.Map {
		return nil, "", 0, "", 0, false
	}
	m := configVal.Map()

	// parse color
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
	// parse stroke
	if s, exists := m["stroke"]; exists {
		stroke = s.Text()
	}
	// parse rounded
	if r, exists := m["rounded"]; exists && r.K == value.Number {
		rounded = r.N
	}

	// parse gradient
	if grad, exists := m["gradient"]; exists {
		if grad.K == value.Map {
			gm := grad.Map()
			if gt, exists := gm["type"]; exists {
				gradType = gt.Text()
			}
			if gc, exists := gm["colors"]; exists {
				var gradColors []string
				if gc.K == value.Array {
					var arr []value.Value
					if p, ok := gc.V.(*[]value.Value); ok && p != nil {
						arr = *p
					} else if s, ok := gc.V.([]value.Value); ok {
						arr = s
					}
					gradColors = make([]string, len(arr))
					for i, v := range arr {
						gradColors[i] = v.Text()
					}
				} else if gc.K == value.String {
					gradColors = strings.Split(gc.Text(), ",")
				}
				if len(gradColors) > 0 {
					colors = gradColors
				}
			}
			if ga, exists := gm["angle"]; exists && ga.K == value.Number {
				gradAngle = ga.N
			} else {
				gradAngle = 45.0
			}
		}
	}
	return colors, stroke, rounded, gradType, gradAngle, true
}

func (q *Qrcode) Finder(args ...value.Value) *Qrcode {
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

	colors, stroke, rounded, gradType, gradAngle, ok := parseFinderConfig(config)
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
			if gradType != "" {
				q.options.Finders.TopLeft.Gradient = &qr.Gradient{Type: gradType, Colors: colors, Angle: gradAngle}
			}
		case "tr":
			if len(colors) > 0 {
				q.options.Finders.TopRight.Color = colors[0]
			}
			q.options.Finders.TopRight.Stroke = stroke
			if rounded != 0 {
				q.options.Finders.TopRight.Rounded = rounded
			}
			if gradType != "" {
				q.options.Finders.TopRight.Gradient = &qr.Gradient{Type: gradType, Colors: colors, Angle: gradAngle}
			}
		case "bl":
			if len(colors) > 0 {
				q.options.Finders.BottomLeft.Color = colors[0]
			}
			q.options.Finders.BottomLeft.Stroke = stroke
			if rounded != 0 {
				q.options.Finders.BottomLeft.Rounded = rounded
			}
			if gradType != "" {
				q.options.Finders.BottomLeft.Gradient = &qr.Gradient{Type: gradType, Colors: colors, Angle: gradAngle}
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

			if rounded != 0 {
				q.options.Finders.TopLeft.Rounded = rounded
				q.options.Finders.TopRight.Rounded = rounded
				q.options.Finders.BottomLeft.Rounded = rounded
			}
			if gradType != "" {
				g := &qr.Gradient{Type: gradType, Colors: colors, Angle: gradAngle}
				q.options.Finders.TopLeft.Gradient = g
				q.options.Finders.TopRight.Gradient = g
				q.options.Finders.BottomLeft.Gradient = g
			}
		}
	}

	apply(position)
	return q
}

func (q *Qrcode) Svg() value.Value {
	svgStr, err := q.options.Svg()
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	return value.New(svgStr)
}

func (q *Qrcode) Png() value.Value {
	pngBytes, err := q.options.Png()
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	return value.New(pngBytes)
}
