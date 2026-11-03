//go:build kitdb_profile

package relational

import (
	"context"
	"testing"
)

func TestPreparedPrimarySelectSingleReservation(t *testing.T) {
	for _, column := range []string{"id", "bucket"} {
		t.Run(column, func(t *testing.T) {
			e := pointSQLFixture(t, 8)
			if err := e.database.EnableLockProfile(); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Execute(context.Background(), "SELECT id FROM items WHERE "+column+"=1"); err != nil {
				t.Fatal(err)
			}
			p := e.database.LockProfile()
			reserve, capture := 0, 0
			for _, sample := range p.Samples {
				switch sample.Site {
				case "snapshot.reserve":
					reserve++
				case "snapshot.capture":
					capture++
				}
			}
			wantCapture := 0
			if column == "bucket" {
				wantCapture = 1
			}
			if p.Dropped != 0 || reserve != 1 || capture != wantCapture {
				t.Fatalf("reserve=%d capture=%d dropped=%d", reserve, capture, p.Dropped)
			}
			assertPrimaryAdmissionDrained(t, e)
		})
	}
}
