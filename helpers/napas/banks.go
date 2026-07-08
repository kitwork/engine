package napas

import (
	"strings"
)

var bankBINs = map[string]string{
	"vcb":         "970436", // Vietcombank
	"tcb":         "970407", // Techcombank
	"mbb":         "970422", // MBBank
	"bidv":        "970418", // BIDV
	"vtb":         "970415", // VietinBank
	"acb":         "970416", // ACB
	"vpbank":      "970432", // VPBank
	"sacombank":   "970403", // Sacombank
	"tpbank":      "970423", // TPBank
	"hdbank":      "970437", // HDBank
	"shb":         "970443", // SHB
	"vib":         "970441", // VIB
	"seabank":     "970440", // SeABank
	"ocb":         "970448", // OCB
	"msb":         "970426", // MSB
	"eximbank":    "970431", // Eximbank
	"scb":         "970429", // SCB
	"lienviet":    "970449", // LPBank (LienVietPostBank)
	"bacabank":    "970409", // Bac A Bank
	"abbank":      "970425", // An Binh Bank
	"pvcombank":   "970412", // PVcomBank
	"dongabank":   "970406", // DongA Bank
	"vietbank":    "970442", // VietBank
	"vietcapital": "970454", // BVBank (Viet Capital Bank)
	"kienlong":    "970452", // Kienlongbank
	"pgbank":      "970430", // PGBank
	"saigonbank":  "970400", // Saigonbank
	"publicbank":  "970439", // Public Bank Vietnam
	"namabank":    "970428", // Nam A Bank
	"shinhan":     "970424", // Shinhan Bank Vietnam
	"woori":       "970457", // Woori Bank Vietnam
	"cimb":        "970458", // CIMB Bank Vietnam
}

func resolveBIN(bankOrBin string) string {
	cleaned := strings.ToLower(strings.TrimSpace(bankOrBin))
	if bin, exists := bankBINs[cleaned]; exists {
		return bin
	}
	return bankOrBin
}
