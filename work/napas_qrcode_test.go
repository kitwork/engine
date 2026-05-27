package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestNapasPayload(t *testing.T) {
	work := &KitWork{}
	napas := work.Napas()

	napas.Bank(value.New("970415"), value.New("1234567890")).
		Amount(value.New(150000)).
		Receiver(value.New("NGUYEN VAN A")).
		Info(value.New("Ung ho"))

	payload := napas.Payload()

	if !strings.HasPrefix(payload, "000201") {
		t.Errorf("expected EMVCo header 000201, got: %s", payload)
	}
	if !strings.Contains(payload, "970415") {
		t.Errorf("expected BIN 970415 in payload, got: %s", payload)
	}
	if !strings.Contains(payload, "1234567890") {
		t.Errorf("expected account number 1234567890 in payload, got: %s", payload)
	}
}

func TestCustomQrcode(t *testing.T) {
	work := &KitWork{}
	qr := work.Qrcode()

	opts := qr.Data(value.New("https://kitwork.io")).
		Template(value.New("circular")).
		CellColor(value.New("#005ba1")).
		Padding(value.New(2))

	svgVal := opts.Svg()
	svgStr := svgVal.Text()

	if !strings.Contains(svgStr, "<svg") || !strings.Contains(svgStr, "</svg>") {
		t.Errorf("expected valid SVG string, got: %s", svgStr)
	}

	pngVal := opts.Png()
	if pngVal.K != value.Bytes {
		t.Errorf("expected PNG to return bytes, got: %s", pngVal.K.String())
	}

	pngBytes, ok := pngVal.V.([]byte)
	if !ok || len(pngBytes) == 0 {
		t.Error("expected valid non-empty PNG bytes slice")
	}
}

func TestVMIntegration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-integration-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	scriptCode := `
	const { napas, qrcode } = kitwork();
	
	const myNapas = napas
		.bank("970415", "1234567890")
		.amount(150000)
		.receiver("NGUYEN VAN A")
		.info("Ung ho");
		
	const svgString = qrcode
		.napas(myNapas)
		.template("circular")
		.padding(2)
		.cell({
			color: "#0f172a",
			size: 0.75,
			gradient: { type: "linear", colors: ["#0f172a", "#38bdf8"], angle: 45 }
		})
		.finder("tl", {
			stroke: "#be123c",
			rounded: 3.5,
			gradient: { type: "linear", colors: ["#e11d48", "#f43f5e"], angle: 90 }
		})
		.finder("tr", "#008800")
		.svg();
		
	resultPayload = myNapas.payload();
	resultSvg = svgString;
	`
	err = os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(scriptCode), 0644)
	if err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	err = tenant.Run()
	if err != nil {
		t.Fatalf("failed to run tenant script: %v", err)
	}

	valPayload, ok := tenant.vm.Vars["resultPayload"]
	if !ok {
		t.Fatal("resultPayload global not found in vm.Vars")
	}
	payloadStr := valPayload.Text()
	if !strings.HasPrefix(payloadStr, "000201") || !strings.Contains(payloadStr, "970415") {
		t.Errorf("unexpected payload: %s", payloadStr)
	}

	valSvg, ok := tenant.vm.Vars["resultSvg"]
	if !ok {
		t.Fatal("resultSvg global not found in vm.Vars")
	}
	svgStr := valSvg.Text()
	if !strings.Contains(svgStr, "<svg") || !strings.Contains(svgStr, "</svg>") {
		t.Errorf("unexpected SVG string: %s", svgStr)
	}
}

func TestQrcodeGradient(t *testing.T) {
	work := &KitWork{}
	qr := work.Qrcode()

	opts := qr.Data(value.New("https://kitwork.io")).
		CellGradient(value.New("linear"), value.New("#ff0000,#00ff00"), value.New(90)).
		FinderGradient(value.New("radial"), value.New([]value.Value{value.New("#0000ff"), value.New("#ffffff")}), value.New(0))

	svgVal := opts.Svg()
	svgStr := svgVal.Text()

	if !strings.Contains(svgStr, "id=\"cell-gradient\"") {
		t.Error("expected cell-gradient definition in SVG")
	}
	if !strings.Contains(svgStr, "id=\"finder-tl-gradient\"") {
		t.Error("expected finder-tl-gradient definition in SVG")
	}
	if !strings.Contains(svgStr, "url(#cell-gradient)") {
		t.Error("expected cells to be filled with cell-gradient")
	}
	if !strings.Contains(svgStr, "url(#finder-tl-gradient)") {
		t.Error("expected finders to reference finder-tl-gradient")
	}
}

func TestAutoContrast(t *testing.T) {
	work := &KitWork{}
	qr := work.Qrcode()

	// Yellow (#ffff00) has very low contrast on white background (#ffffff)
	opts := qr.Data(value.New("https://kitwork.io")).
		CellColor(value.New("#ffff00")).
		FinderColor(value.New("#fffa00")).
		FinderStroke(value.New("#fff900"))

	svgVal := opts.Svg()
	svgStr := svgVal.Text()

	// It should auto-adjust the color to safe dark color (e.g., #000000)
	if strings.Contains(svgStr, "fill=\"#ffff00\"") {
		t.Error("expected yellow cell color to be adjusted for contrast, but found raw color in SVG")
	}
	if !strings.Contains(svgStr, "fill=\"#000000\"") {
		t.Error("expected cell color to fall back to safe black color")
	}
}

func TestVietQRLogo(t *testing.T) {
	work := &KitWork{}
	qr := work.Qrcode()

	opts := qr.Data(value.New("https://kitwork.io")).
		Logo(value.New("vietqr"))

	svgVal := opts.Svg()
	svgStr := svgVal.Text()

	if !strings.Contains(svgStr, "scale(") || !strings.Contains(svgStr, "rect") {
		t.Error("expected embedded VietQR vector logo in SVG")
	}
}

func TestIndividualFinders(t *testing.T) {
	work := &KitWork{}
	qr := work.Qrcode()

	opts := qr.Data(value.New("https://kitwork.io")).
		Finder(value.New("tl"), value.New(map[string]value.Value{
			"color":   value.New("#ff0000"),
			"stroke":  value.New("#aa0000"),
			"rounded": value.New(3.5),
		})).
		Finder(value.New("tr"), value.New(map[string]value.Value{
			"color":   value.New("#008800"),
			"stroke":  value.New("#00aa00"),
			"rounded": value.New(1.5),
		})).
		Finder(value.New("bl"), value.New(map[string]value.Value{
			"color":   value.New("#0000ff"),
			"stroke":  value.New("#0000aa"),
			"rounded": value.New(0),
		}))

	svgVal := opts.Svg()
	svgStr := svgVal.Text()

	// Check that custom strokes and fills are present in the SVG
	if !strings.Contains(svgStr, "stroke=\"#aa0000\"") || !strings.Contains(svgStr, "fill=\"#ff0000\"") {
		t.Error("expected custom TL finder stroke and fill in SVG")
	}
	if !strings.Contains(svgStr, "stroke=\"#00aa00\"") || !strings.Contains(svgStr, "fill=\"#008800\"") {
		t.Error("expected custom TR finder stroke and fill in SVG")
	}
	if !strings.Contains(svgStr, "stroke=\"#0000aa\"") || !strings.Contains(svgStr, "fill=\"#0000ff\"") {
		t.Error("expected custom BL finder stroke and fill in SVG")
	}

	// Gradient individual finders test
	optsGrad := qr.Data(value.New("https://kitwork.io")).
		Finder(value.New("tl"), value.New(map[string]value.Value{
			"gradient": value.New(map[string]value.Value{
				"type":   value.New("linear"),
				"colors": value.New("#ff0000,#ffff00"),
				"angle":  value.New(90),
			}),
		})).
		Finder(value.New("tr"), value.New(map[string]value.Value{
			"gradient": value.New(map[string]value.Value{
				"type":   value.New("radial"),
				"colors": value.New("#00ff00,#00ffff"),
				"angle":  value.New(0),
			}),
		})).
		Finder(value.New("bl"), value.New(map[string]value.Value{
			"gradient": value.New(map[string]value.Value{
				"type":   value.New("linear"),
				"colors": value.New("#0000ff,#ff00ff"),
				"angle":  value.New(180),
			}),
		}))

	svgGradVal := optsGrad.Svg()
	svgGradStr := svgGradVal.Text()

	if !strings.Contains(svgGradStr, "id=\"finder-tl-gradient\"") || !strings.Contains(svgGradStr, "url(#finder-tl-gradient)") {
		t.Error("expected custom TL finder gradient in SVG")
	}
	if !strings.Contains(svgGradStr, "id=\"finder-tr-gradient\"") || !strings.Contains(svgGradStr, "url(#finder-tr-gradient)") {
		t.Error("expected custom TR finder gradient in SVG")
	}
	if !strings.Contains(svgGradStr, "id=\"finder-bl-gradient\"") || !strings.Contains(svgGradStr, "url(#finder-bl-gradient)") {
		t.Error("expected custom BL finder gradient in SVG")
	}
}

func TestSmartAPIs(t *testing.T) {
	work := &KitWork{}
	qr := work.Qrcode()
	napas := work.Napas()

	napas.Bank(value.New("970415"), value.New("1234567890")).Amount(value.New(200000))

	// Create map configurations for cell and finder
	cellMap := map[string]value.Value{
		"color": value.New("#0f172a"),
		"size":  value.New(0.75),
		"gradient": value.New(map[string]value.Value{
			"type":   value.New("linear"),
			"colors": value.New("#0f172a,#38bdf8"),
			"angle":  value.New(45),
		}),
	}

	finderTLMap := map[string]value.Value{
		"stroke":  value.New("#be123c"),
		"rounded": value.New(3.5),
		"gradient": value.New(map[string]value.Value{
			"type":   value.New("linear"),
			"colors": value.New("#e11d48,#f43f5e"),
			"angle":  value.New(90),
		}),
	}

	opts := qr.Napas(value.New(napas)).
		Template(value.New("circular")).
		Logo(value.New("vietqr")).
		Cell(value.New(cellMap)).
		Finder(value.New("tl"), value.New(finderTLMap)).
		Finder(value.New("tr"), value.New("#008800"))

	svgVal := opts.Svg()
	svgStr := svgVal.Text()

	if !strings.Contains(svgStr, "<svg") || !strings.Contains(svgStr, "</svg>") {
		t.Error("expected valid SVG string")
	}
	if !strings.Contains(svgStr, "id=\"cell-gradient\"") {
		t.Error("expected cell gradient in SVG")
	}
	if !strings.Contains(svgStr, "id=\"finder-tl-gradient\"") {
		t.Error("expected TL finder gradient in SVG")
	}
	if !strings.Contains(svgStr, "fill=\"#008800\"") {
		t.Error("expected TR finder color in SVG")
	}
}

func TestSmartDataInputs(t *testing.T) {
	work := &KitWork{}
	qr := work.Qrcode()

	// 1. WiFi object
	wifiMap := map[string]value.Value{
		"ssid":       value.New("MyWiFi"),
		"password":   value.New("12345678"),
		"encryption": value.New("WPA2"),
	}
	opts1 := qr.Data(value.New(wifiMap))
	if opts1.options.Data != "WIFI:S:MyWiFi;T:WPA2;P:12345678;H:false;;" {
		t.Errorf("expected WiFi payload, got: %s", opts1.options.Data)
	}

	// 2. Nested WiFi object
	nestedWifi := map[string]value.Value{
		"wifi": value.New(map[string]value.Value{
			"ssid": value.New("NestedWiFi"),
		}),
	}
	opts2 := qr.Data(value.New(nestedWifi))
	if opts2.options.Data != "WIFI:S:NestedWiFi;T:nopass;P:;H:false;;" {
		t.Errorf("expected Nested WiFi payload, got: %s", opts2.options.Data)
	}

	// 3. Napas-like object
	napasMap := map[string]value.Value{
		"bank":    value.New("970415"),
		"number":  value.New("111222333"),
		"so_tien": value.New(50000),
		"desc":    value.New("test-napas-like"),
	}
	opts3 := qr.Data(value.New(napasMap))
	if !strings.HasPrefix(opts3.options.Data, "000201") || !strings.Contains(opts3.options.Data, "970415") || !strings.Contains(opts3.options.Data, "111222333") {
		t.Errorf("expected Napas payload from Napas-like map, got: %s", opts3.options.Data)
	}

	// 4. Nested Napas object
	nestedNapas := map[string]value.Value{
		"napas": value.New(map[string]value.Value{
			"bin":     value.New("970415"),
			"account": value.New("111222333"),
		}),
	}
	opts4 := qr.Data(value.New(nestedNapas))
	if !strings.HasPrefix(opts4.options.Data, "000201") || !strings.Contains(opts4.options.Data, "970415") || !strings.Contains(opts4.options.Data, "111222333") {
		t.Errorf("expected Napas payload from nested map, got: %s", opts4.options.Data)
	}

	// 5. Mail object
	mailMap := map[string]value.Value{
		"email":   value.New("test@example.com"),
		"subject": value.New("Greeting User"),
		"body":    value.New("Hello World"),
	}
	opts5 := qr.Data(value.New(mailMap))
	if opts5.options.Data != "mailto:test@example.com?subject=Greeting%20User&body=Hello%20World" {
		t.Errorf("expected mailto payload, got: %s", opts5.options.Data)
	}

	// 6. SMS object
	smsMap := map[string]value.Value{
		"phone":   value.New("0987654321"),
		"message": value.New("Hi Antigravity"),
	}
	opts6 := qr.Data(value.New(smsMap))
	if opts6.options.Data != "SMSTO:0987654321:Hi Antigravity" {
		t.Errorf("expected SMSTO payload, got: %s", opts6.options.Data)
	}

	// 7. Phone object
	phoneMap := map[string]value.Value{
		"phone": value.New("0987654321"),
	}
	opts7 := qr.Data(value.New(phoneMap))
	if opts7.options.Data != "tel:0987654321" {
		t.Errorf("expected tel payload, got: %s", opts7.options.Data)
	}

	// 8. Geolocation object
	geoMap := map[string]value.Value{
		"lat": value.New("10.7769"),
		"lng": value.New("106.7009"),
	}
	opts8 := qr.Data(value.New(geoMap))
	if opts8.options.Data != "geo:10.7769,106.7009" {
		t.Errorf("expected geo payload, got: %s", opts8.options.Data)
	}

	// 9. Fallback link extraction
	linkMap := map[string]value.Value{
		"link": value.New("https://kitwork.io"),
	}
	opts9 := qr.Data(value.New(linkMap))
	if opts9.options.Data != "https://kitwork.io" {
		t.Errorf("expected URL payload from link map, got: %s", opts9.options.Data)
	}
}

func TestUnifiedFinder(t *testing.T) {
	work := &KitWork{}
	qr := work.Qrcode()

	// 1. Without position -> apply to all three
	configMap := map[string]value.Value{
		"color":   value.New("#008800"),
		"stroke":  value.New("#00aa00"),
		"rounded": value.New(2.5),
	}
	opts1 := qr.Data(value.New("test")).Finder(value.New(configMap))

	if opts1.options.Finders.TopLeft.Color != "#008800" || opts1.options.Finders.TopRight.Color != "#008800" || opts1.options.Finders.BottomLeft.Color != "#008800" {
		t.Errorf("expected all finders to be set to #008800, got TL=%s, TR=%s, BL=%s",
			opts1.options.Finders.TopLeft.Color, opts1.options.Finders.TopRight.Color, opts1.options.Finders.BottomLeft.Color)
	}
	if opts1.options.Finders.TopLeft.Rounded != 2.5 || opts1.options.Finders.TopRight.Rounded != 2.5 || opts1.options.Finders.BottomLeft.Rounded != 2.5 {
		t.Errorf("expected all finders rounded to be 2.5, got TL=%.1f, TR=%.1f, BL=%.1f",
			opts1.options.Finders.TopLeft.Rounded, opts1.options.Finders.TopRight.Rounded, opts1.options.Finders.BottomLeft.Rounded)
	}

	// 2. With position -> apply to that position only
	configTLMap := map[string]value.Value{
		"color":    value.New("#ff0000"),
		"position": value.New("tl"),
	}
	opts2 := qr.Data(value.New("test")).
		Finder(value.New(configMap)).
		Finder(value.New(configTLMap))

	if opts2.options.Finders.TopLeft.Color != "#ff0000" {
		t.Errorf("expected TL finder to be overridden to #ff0000, got: %s", opts2.options.Finders.TopLeft.Color)
	}
	if opts2.options.Finders.TopRight.Color != "#008800" {
		t.Errorf("expected TR finder to remain #008800, got: %s", opts2.options.Finders.TopRight.Color)
	}
}

func TestSmartLogo(t *testing.T) {
	work := &KitWork{}
	qr := work.Qrcode()

	logoConfig := map[string]value.Value{
		"logo":       value.New("vietqr"),
		"background": value.New("#ff0000"),
		"shape":      value.New("circle"),
		"size":       value.New(0.25),
		"padding":    value.New(0.5),
	}

	opts := qr.Data(value.New("test-logo")).Center(value.New(logoConfig))
	svgStr := opts.Svg().Text()

	// Check if circle background is drawn
	if !strings.Contains(svgStr, "<circle") || !strings.Contains(svgStr, "fill=\"#ff0000\"") {
		t.Error("expected circular red background for smart logo in SVG")
	}

	// Verify size and path are correctly saved
	if opts.options.Center.Logo != "vietqr" || opts.options.Center.Shape != "circle" || opts.options.Center.Size != 0.25 || opts.options.Center.Padding != 0.5 {
		t.Errorf("center config was not correctly parsed: %+v", opts.options.Center)
	}
}

func TestSmartDataContact(t *testing.T) {
	work := &KitWork{}
	qr := work.Qrcode()

	contactMap := map[string]value.Value{
		"name":    value.New("Nguyen Van A"),
		"phone":   value.New("0987654321"),
		"email":   value.New("a@example.com"),
		"company": value.New("Kitwork LLC"),
		"title":   value.New("Software Engineer"),
		"url":     value.New("https://kitwork.io"),
	}

	opts := qr.Data(value.New(contactMap))

	if !strings.Contains(opts.options.Data, "BEGIN:VCARD") || !strings.Contains(opts.options.Data, "END:VCARD") {
		t.Error("expected contact map to be parsed into vCard format")
	}
	if !strings.Contains(opts.options.Data, "FN:Nguyen Van A") || !strings.Contains(opts.options.Data, "TEL;TYPE=CELL:0987654321") {
		t.Error("vCard fields were not correctly written")
	}
}

func TestMergeToggle(t *testing.T) {
	work := &KitWork{}
	qr := work.Qrcode()

	// Case 1: Merge = true (default)
	optsMerged := qr.Data(value.New("https://kitwork.io")).
		Template(value.New("circle"))
	svgMerged := optsMerged.Svg().Text()

	if !strings.Contains(svgMerged, "<path") {
		t.Error("expected merged path in SVG by default")
	}
	if strings.Contains(svgMerged, "<circle") {
		t.Error("should not contain individual circle tags when merged")
	}

	// Case 2: Merge = false (individual cells)
	optsIndividual := qr.Data(value.New("https://kitwork.io")).
		Template(value.New("circle")).
		Merge(value.New(false))
	svgIndividual := optsIndividual.Svg().Text()

	if !strings.Contains(svgIndividual, "<circle") {
		t.Error("expected individual circle tags in SVG when merge is disabled")
	}
}





