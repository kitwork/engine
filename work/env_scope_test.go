package work

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// An app reads its own environment and nothing above or beside it (docs/KITWORK_ENVIRONMENT.md §1,
// 22/10/2026, environment.KEY). The folder that holds every app (apps/) may carry a .env,
// another app beside this one certainly does, and the operating system has variables of its own; none may
// reach a request of this app, whether its site has no .env, an empty one or one of its own, with the app's
// own .env at the app's folder or not. The app's own keys are read back too, so a broken environment cannot
// pass as an isolated one.
//
// CONTROL (22/10/2026, run twice): with a fallback put into work/tenant.go reading ../.env of the app's
// folder when its own is missing (the fallback capabilities/env had), the rows with no app .env read
// apps/.env: "the app read a .env that is not its own".
func TestEnvOfAnAppStopsAtItsRoot(t *testing.T) {
	const router = `
import { router, environment } from "kitwork";

router.get((ctx) => ctx.json({ own: environment.OWN, app: environment.APP_OWN, apps: environment.FROM_APPS_FOLDER,
  neighbour: environment.NEIGHBOUR_SECRET, system: environment.KITWORK_TEST_FROM_THE_SYSTEM }));
`
	t.Setenv("KITWORK_TEST_FROM_THE_SYSTEM", "leaked")
	for _, appEnv := range []string{"missing", "APP_OWN=app\n"} {
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
			write("victim-corp/.env", "NEIGHBOUR_SECRET="+victimMarker+"\n")
			write("victim-corp/victim.com/.env", "NEIGHBOUR_SECRET="+victimMarker+"\n")
			write("acme/localhost/router.kitwork.js", router)
			if ownEnv != "missing" {
				write("acme/localhost/.env", ownEnv)
			}
			if appEnv != "missing" {
				write("acme/.env", appEnv)
			}

			tenant := NewTenant(root, "localhost")
			if err := tenant.Run(); err != nil {
				t.Fatal(err)
			}
			rec := serveIsolation(t, tenant, "http://localhost/")
			tenant.Close()
			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("app .env %q, site .env %q: response %d %q is not JSON: %v", appEnv, ownEnv, rec.Code, rec.Body.String(), err)
			}
			if got["apps"] != nil || got["neighbour"] != nil || got["system"] != nil {
				t.Errorf("app .env %q, site .env %q: the app read a .env that is not its own: %v", appEnv, ownEnv, got)
			}
			wantOwn, wantApp := any(nil), any(nil)
			if ownEnv == "OWN=mine\n" {
				wantOwn = "mine"
			}
			if appEnv != "missing" {
				wantApp = "app"
			}
			if got["own"] != wantOwn || got["app"] != wantApp {
				t.Errorf("app .env %q, site .env %q: environment.OWN = %v, APP_OWN = %v, want %v, %v", appEnv, ownEnv, got["own"], got["app"], wantOwn, wantApp)
			}
		}
	}
}
