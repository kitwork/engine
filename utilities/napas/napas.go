package napas

import (
	"fmt"
)

type Napas struct {
	Bin           string `json:"bin"`
	AccountNumber string `json:"account_number"`
	AmountVal     string `json:"amount"`
	AddInfo       string `json:"add_info"`
	CountryCode   string `json:"country_code"`
	Method        string `json:"method"`
	ServiceCode   string `json:"service_code"`
	MerchantName  string `json:"merchant_name"`
	MerchantCity  string `json:"merchant_city"`
}

func New() *Napas {
	return &Napas{
		CountryCode: "VN",
		Method:      "11",
		ServiceCode: "QRIBFTTA",
	}
}

func (n *Napas) Validate() error {
	if n.Bin == "" {
		return fmt.Errorf("napas: bank BIN is required")
	}
	if len(n.Bin) != 6 {
		return fmt.Errorf("napas: bank BIN must be exactly 6 digits, got %q", n.Bin)
	}
	for _, c := range n.Bin {
		if c < '0' || c > '9' {
			return fmt.Errorf("napas: bank BIN must contain only digits, got %q", n.Bin)
		}
	}
	if n.AccountNumber == "" {
		return fmt.Errorf("napas: account number is required")
	}
	return nil
}

func (n *Napas) Bank(bin string, accountNumber string) *Napas {
	n.Bin = resolveBIN(bin)
	n.AccountNumber = accountNumber
	return n
}

func (n *Napas) Amount(v string) *Napas {
	n.AmountVal = v
	return n
}

func (n *Napas) Info(v string) *Napas {
	n.AddInfo = v
	return n
}

func (n *Napas) Service(v string) *Napas {
	n.ServiceCode = v
	return n
}

func (n *Napas) ToAccount() *Napas {
	n.ServiceCode = "QRIBFTTA"
	return n
}

func (n *Napas) ToCard() *Napas {
	n.ServiceCode = "QRIBFTTC"
	return n
}

func (n *Napas) Receiver(v string) *Napas {
	n.MerchantName = v
	return n
}

func (n *Napas) ReceiverName(v string) *Napas {
	return n.Receiver(v)
}

func (n *Napas) City(v string) *Napas {
	n.MerchantCity = v
	return n
}

func (n *Napas) Dynamic() *Napas {
	n.Method = "12"
	return n
}

func (n *Napas) Static() *Napas {
	n.Method = "11"
	return n
}

func (n *Napas) Country(v string) *Napas {
	n.CountryCode = v
	return n
}

func (n *Napas) Payload() string {
	data := emv("00", "01") + // Payload Format Indicator
		emv("01", n.Method) + // Point of Initiation Method
		emv("38", // Consumer Account Information
			emv("00", "A000000727")+ // NAPAS AID
				emv("01", emv("00", n.Bin)+emv("01", n.AccountNumber))+
				emv("02", n.ServiceCode),
		) +
		emv("53", "704") // Transaction Currency (VND)

	if n.AmountVal != "" {
		data += emv("54", n.AmountVal) // Transaction Amount
	}

	data += emv("58", n.CountryCode) // Country Code

	if n.MerchantName != "" {
		data += emv("59", n.MerchantName) // Merchant Name
	}
	if n.MerchantCity != "" {
		data += emv("60", n.MerchantCity) // Merchant City
	}

	// Add additional info if provided
	if n.AddInfo != "" {
		data += emv("62", emv("08", n.AddInfo))
	}

	data += "6304"                 // CRC tag
	return data + crc16CCITT(data) // Append calculated CRC16
}

func (n *Napas) Generate() (string, error) {
	if err := n.Validate(); err != nil {
		return "", err
	}
	return n.Payload(), nil
}
