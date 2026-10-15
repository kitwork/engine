package site

import (
	"reflect"
	"testing"

	jitcss "github.com/kitwork/engine/jit/css"
)

// A clone of a CSS configuration shares no map or slice with its source: the render plan merges a
// branch's router.colors() and router.fonts() into a clone of the site's configuration, and a shared
// FontFamily let one branch's typeface replace the site's own. Every map and slice field is checked,
// so a field added later is covered too.
func TestCloneJITConfigSharesNothing(t *testing.T) {
	source := cloneJITConfig(&jitcss.DefaultConfig)
	source.FontFamily = map[string]string{"body": "Outfit"}
	source.HighlightPalette = map[string]string{"keyword": "#ff0000"}
	source.Themes = map[string]map[string]jitcss.Color{"dark": {"canvas": jitcss.Hex("#141110")}}

	clone := CloneJITConfig(source)
	a, b := reflect.ValueOf(source).Elem(), reflect.ValueOf(clone).Elem()
	checked := 0
	for i := 0; i < a.NumField(); i++ {
		field := a.Type().Field(i)
		x, y := a.Field(i), b.Field(i)
		switch field.Type.Kind() {
		case reflect.Map, reflect.Slice:
			if x.IsNil() || (field.Type.Kind() == reflect.Slice && x.Len() == 0) {
				continue
			}
			checked++
			if x.Pointer() == y.Pointer() {
				t.Errorf("the clone shares %s with its source", field.Name)
			}
		}
	}
	if checked < 10 {
		t.Fatalf("control: only %d map or slice fields were set, the check proves little", checked)
	}

	clone.FontFamily["body"] = "Inter"
	clone.Themes["dark"]["canvas"] = jitcss.Hex("#000000")
	if source.FontFamily["body"] != "Outfit" {
		t.Errorf("a typeface written into the clone reached the source: %q", source.FontFamily["body"])
	}
	if source.Themes["dark"]["canvas"] != jitcss.Hex("#141110") {
		t.Error("a dark value written into the clone reached the source")
	}
}
