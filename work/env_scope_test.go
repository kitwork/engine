package work

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// An app reads its own .env and nothing above or beside it (docs/KITWORK_ENVIRONMENT.md §1,
// 22/10/2026). The folder that holds every app (apps/) may carry a .env, and another app beside this
// one certainly does; neither may reach a request of this app, whether it has no .env at all or an
// empty one. The app's own key is read back too, so a broken env cannot pass as an isolated one.
func TestEnvOfAnAppStopsAtItsRoot(t *testing.T) {
	const router = `
import { router, env } from "kitwork";

router.get((ctx) => ctx.json({ own: env.OWN, apps: env.FROM_APPS_FOLDER, neighbour: env.NEIGHBOUR_SECRET }));
`
	for _, ownEnv := range []string{"", "missing", "OWN=mine\n"} {
		root := t.TempDir()
		write := func(path, body string) {
			t.Helper()
			full := filepath.Join(root, path)
			if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
		}
		write(".env", "FROM_APPS_FOLDER=leaked\nNEIGHBOUR_SECRET=leaked\n")
		write("victim-corp/victim.com/.env", "NEIGHBOUR_SECRET="+victimMarker+"\n")
		write("acme/localhost/router.kitwork.js", router)
		if ownEnv != "missing" {
			write("acme/localhost/.env", ownEnv)
		}

		tenant := NewTenant(root, "localhost")
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
		rec := serveIsolation(t, tenant, "http://localhost/")
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf(".env %q: response %d %q is not JSON: %v", ownEnv, rec.Code, rec.Body.String(), err)
		}
		if got["apps"] != nil || got["neighbour"] != nil {
			t.Errorf(".env %q: the app read a .env that is not its own: %v", ownEnv, got)
		}
		wantOwn := any(nil)
		if ownEnv == "OWN=mine\n" {
			wantOwn = "mine"
		}
		if got["own"] != wantOwn {
			t.Errorf(".env %q: env.OWN = %v, want %v", ownEnv, got["own"], wantOwn)
		}
	}
}
