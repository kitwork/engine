package runtime

import "testing"

// A slot (14/10/2026) reads back as the variable the source wrote; an iteration's object is no variable.
func TestSlotNameIsTheVariableAsWritten(t *testing.T) {
	for slot, want := range map[string]string{
		"error":    "error",
		"error@3":  "error",
		"error#17": "error",
		"#4":       "",
		"$cart@2":  "$cart",
	} {
		if got := SlotName(slot); got != want {
			t.Fatalf("SlotName(%q) = %q, want %q", slot, got, want)
		}
	}
	if !isIterationSlot("#4") || isIterationSlot("x#4") || isIterationSlot("#") {
		t.Fatal("only #N is an iteration's object")
	}
}
