package napas

import (
	"strings"
	"testing"
)

func TestNapasPayload(t *testing.T) {
	n := New()
	n.Bank("970415", "1234567890").
		Amount("150000").
		Receiver("NGUYEN VAN A").
		Info("Ung ho")

	payload := n.Payload()

	if !strings.HasPrefix(payload, "000201") {
		t.Errorf("expected EMVCo header 000201, got: %s", payload)
	}
	if !strings.Contains(payload, "970415") {
		t.Errorf("expected BIN 970415 in payload, got: %s", payload)
	}
	if !strings.Contains(payload, "1234567890") {
		t.Errorf("expected account number 1234567890 in payload, got: %s", payload)
	}

	// Verify exact CRC16 suffix (should be 4 hex digits)
	crcSuffix := payload[len(payload)-4:]
	if len(crcSuffix) != 4 {
		t.Errorf("invalid CRC suffix length: %s", crcSuffix)
	}
}

func TestBankResolution(t *testing.T) {
	// Test mapping abbreviation to BIN
	n := New()
	n.Bank("VCB", "111222333")
	if n.Bin != "970436" {
		t.Errorf("expected VCB to resolve to 970436, got %s", n.Bin)
	}

	n.Bank(" tcb ", "111222333")
	if n.Bin != "970407" {
		t.Errorf("expected tcb to resolve to 970407, got %s", n.Bin)
	}

	// Test unmapped code remains as-is
	n.Bank("970499", "111222333")
	if n.Bin != "970499" {
		t.Errorf("expected unknown bank 970499 to remain unchanged, got %s", n.Bin)
	}
}

func TestServicePresets(t *testing.T) {
	n := New()
	if n.ServiceCode != "QRIBFTTA" {
		t.Errorf("expected default service code to be QRIBFTTA, got %s", n.ServiceCode)
	}

	n.ToCard()
	if n.ServiceCode != "QRIBFTTC" {
		t.Errorf("expected service code to change to QRIBFTTC, got %s", n.ServiceCode)
	}

	n.ToAccount()
	if n.ServiceCode != "QRIBFTTA" {
		t.Errorf("expected service code to change to QRIBFTTA, got %s", n.ServiceCode)
	}
}

func TestOptimizedEMV(t *testing.T) {
	res := emv("01", "hello")
	expected := "0105hello"
	if res != expected {
		t.Errorf("expected %s, got %s", expected, res)
	}

	res2 := emv("54", "10000000000") // 11 chars
	expected2 := "541110000000000"
	if res2 != expected2 {
		t.Errorf("expected %s, got %s", expected2, res2)
	}
}

func TestNapasValidation(t *testing.T) {
	// Test empty BIN
	n1 := New()
	n1.Bank("", "12345")
	if err := n1.Validate(); err == nil || !strings.Contains(err.Error(), "BIN is required") {
		t.Errorf("expected error for empty BIN, got: %v", err)
	}

	// Test invalid BIN length
	n2 := New()
	n2.Bank("97041", "12345")
	if err := n2.Validate(); err == nil || !strings.Contains(err.Error(), "must be exactly 6 digits") {
		t.Errorf("expected error for invalid BIN length, got: %v", err)
	}

	// Test non-numeric BIN
	n3 := New()
	n3.Bank("97041A", "12345")
	if err := n3.Validate(); err == nil || !strings.Contains(err.Error(), "must contain only digits") {
		t.Errorf("expected error for non-numeric BIN, got: %v", err)
	}

	// Test empty Account Number
	n4 := New()
	n4.Bank("VCB", "")
	if err := n4.Validate(); err == nil || !strings.Contains(err.Error(), "account number is required") {
		t.Errorf("expected error for empty account number, got: %v", err)
	}

	// Test valid configuration
	n5 := New()
	n5.Bank("VCB", "12345")
	if err := n5.Validate(); err != nil {
		t.Errorf("expected no error for valid config, got: %v", err)
	}
}

func TestNapasGenerateValidation(t *testing.T) {
	// Generate on invalid setup should fail
	n := New()
	n.Bank("VCB", "")
	_, err := n.Generate()
	if err == nil {
		t.Error("expected Generate to fail due to validation error")
	}

	// Generate on valid setup should succeed
	n.Bank("VCB", "123456789")
	val, err := n.Generate()
	if err != nil {
		t.Errorf("expected Generate to succeed, got: %v", err)
	}
	if !strings.Contains(val, "970436") {
		t.Errorf("expected generated payload to contain VCB BIN, got: %s", val)
	}
}
