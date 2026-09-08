package css

import (
	"strings"
	"testing"
)

func TestScrollbarButtonStartVariantReusesCoreUtilities(t *testing.T) {
	tests := []struct {
		className string
		wantCSS   string
		wantSel   string
	}{
		{
			className: "scrollbar:w-2.5",
			wantCSS:   "width: 0.625rem;",
			wantSel:   `.scrollbar\:w-2\.5::-webkit-scrollbar`,
		},
		{
			className: "scrollbar-track:bg-stone-50",
			wantCSS:   "background-color: rgb(250, 250, 249);",
			wantSel:   `.scrollbar-track\:bg-stone-50::-webkit-scrollbar-track`,
		},
		{
			className: "scrollbar-thumb:bg-zinc-400",
			wantCSS:   "background-color: rgb(161, 161, 170);",
			wantSel:   `.scrollbar-thumb\:bg-zinc-400::-webkit-scrollbar-thumb`,
		},
		{
			className: "scrollbar-thumb:rounded-full",
			wantCSS:   "border-radius: 9999px;",
			wantSel:   `.scrollbar-thumb\:rounded-full::-webkit-scrollbar-thumb`,
		},
		{
			className: "scrollbar-button-start:h-10",
			wantCSS:   "height: 2.5rem;",
			wantSel:   `.scrollbar-button-start\:h-10::-webkit-scrollbar-button:vertical:decrement`,
		},
		{
			className: "scrollbar-button-start:opacity-0",
			wantCSS:   "opacity: 0.00;",
			wantSel:   `.scrollbar-button-start\:opacity-0::-webkit-scrollbar-button:vertical:decrement`,
		},
	}
	for _, test := range tests {
		css, selector, media := ResolveCore(test.className, nil)
		if css != test.wantCSS || selector != test.wantSel || media != "" {
			t.Errorf(
				"ResolveCore(%q) = css %q, selector %q, media %q; want %q, %q, none",
				test.className, css, selector, media, test.wantCSS, test.wantSel,
			)
		}
	}

	generated := GenerateSiteCSS(nil, `<html class="scrollbar:w-2.5 scrollbar-track:bg-stone-50 scrollbar-thumb:rounded-full scrollbar-thumb:bg-zinc-400 dark:scrollbar-track:bg-zinc-950 dark:scrollbar-thumb:bg-zinc-600 scrollbar-button-start:h-10 scrollbar-button-start:opacity-0"></html>`)
	for _, want := range []string{
		`.scrollbar\:w-2\.5::-webkit-scrollbar { width: 0.625rem; }`,
		`.scrollbar-track\:bg-stone-50::-webkit-scrollbar-track { background-color: rgb(250, 250, 249); }`,
		`.scrollbar-thumb\:bg-zinc-400::-webkit-scrollbar-thumb { background-color: rgb(161, 161, 170); }`,
		`.scrollbar-thumb\:rounded-full::-webkit-scrollbar-thumb { border-radius: 9999px; }`,
		`.scrollbar-button-start\:h-10::-webkit-scrollbar-button:vertical:decrement { height: 2.5rem; }`,
		`.scrollbar-button-start\:opacity-0::-webkit-scrollbar-button:vertical:decrement { opacity: 0.00; }`,
	} {
		if !strings.Contains(generated, want) {
			t.Fatalf("generated CSS omitted %q:\n%s", want, generated)
		}
	}
}
