package runtime

import (
	"testing"

	"github.com/kitwork/engine/value"
)

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

// A view's map read back by name: a renamed declaration is its name (over a parameter's own entry of
// that name), a block's variable and an iteration's object are not the view's, and a map without
// slots comes back as it is.
func TestDeclaredNamesReadsSlotsBack(t *testing.T) {
	plain := map[string]value.Value{"title": value.New("a")}
	if got := DeclaredNames(plain); len(got) != 1 || got["title"].Text() != "a" {
		t.Fatalf("plain map: %v", got)
	}
	plain["other"] = value.New(1)
	if got := DeclaredNames(plain); got["other"].N != 1 {
		t.Fatal("a map without slots must be handed over itself")
	}

	slotted := map[string]value.Value{
		"ctx":     value.New("parameter as passed"),
		"ctx@4":   value.New("parameter as assigned"),
		"title@2": value.New("view"),
		"inner#3": value.New(1),
		"#5":      value.New(map[string]value.Value{}),
		"count":   value.New(2),
	}
	got := DeclaredNames(slotted)
	if got["ctx"].Text() != "parameter as assigned" || got["title"].Text() != "view" || got["count"].N != 2 {
		t.Fatalf("names: %v", got)
	}
	if len(got) != 3 {
		t.Fatalf("names hold a block's variable or an iteration's object: %v", got)
	}
}
