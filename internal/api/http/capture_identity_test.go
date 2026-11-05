package httpapi_test

import (
	"encoding/json"
	"testing"

	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
)

func TestCaptureIdentityIncludesBucket(t *testing.T) {
	for _, installation := range []string{"", "gh_inst_1"} {
		for _, together := range []bool{false, true} {
			t.Run(installation+map[bool]string{false: "/separate", true: "/batch"}[together], func(t *testing.T) {
				e := newEngine(t)
				left := e.repository("left", "ev_seed_left", "Left scope")
				right := e.repository("right", "ev_seed_right", "Right scope")
				batch := func(bucket, suffix string) store.Batch {
					return store.Batch{
						Entities: []record.Entity{{ID: "pl_" + suffix, Bucket: bucket, Class: record.PlaceClass, Type: "github.repository",
							Source: &record.Source{System: "github", Installation: installation, NativeID: "repository"}}},
						Events: []record.Event{{ID: "ev_" + suffix, Bucket: bucket, Type: "github.pull_request", Place: "pl_" + suffix,
							Source: &record.Source{System: "github", Installation: installation, NativeID: "delivery"}}},
					}
				}
				join := func(a, b store.Batch) store.Batch {
					a.Entities = append(a.Entities, b.Entities...)
					a.Events = append(a.Events, b.Events...)
					return a
				}
				write := func(b store.Batch, want map[string]string) {
					t.Helper()
					var out store.Written
					if err := json.Unmarshal(e.must(200, "POST", "/v1/records", e.key, "", b), &out); err != nil {
						t.Fatal(err)
					}
					for id, stored := range want {
						if out.Stored[id] != stored {
							t.Fatalf("%s resolved to %q, want %q", id, out.Stored[id], stored)
						}
					}
				}
				if together {
					write(join(join(batch(left, "left"), batch(right, "right")), batch(left, "alias")),
						map[string]string{"pl_left": "pl_left", "ev_left": "ev_left", "pl_right": "pl_right", "ev_right": "ev_right", "pl_alias": "pl_left", "ev_alias": "ev_left"})
				} else {
					write(batch(left, "left"), map[string]string{"pl_left": "pl_left", "ev_left": "ev_left"})
					write(batch(right, "right"), map[string]string{"pl_right": "pl_right", "ev_right": "ev_right"})
				}
				write(join(batch(left, "again"), batch(left, "twice")),
					map[string]string{"pl_again": "pl_left", "ev_again": "ev_left", "pl_twice": "pl_left", "ev_twice": "ev_left"})
				e.readers(left, "gh_ana")
				e.readers(right, "gh_ben")
				ana := e.sign(e.claims("ana", "gh_ana"))
				for _, suffix := range []string{"left", "right"} {
					status := 200
					if suffix == "right" {
						status = 404
					}
					e.must(status, "GET", "/v1/entities/pl_"+suffix, e.key, ana, nil)
					e.must(status, "GET", "/v1/events/ev_"+suffix, e.key, ana, nil)
					var event record.Event
					if err := json.Unmarshal(e.must(200, "GET", "/v1/events/ev_"+suffix, e.key, "", nil), &event); err != nil {
						t.Fatal(err)
					}
					if event.Place != "pl_"+suffix || event.Bucket != map[string]string{"left": left, "right": right}[suffix] {
						t.Fatalf("event references another scope: %+v", event)
					}
				}
				e.must(403, "POST", "/v1/records", e.key, ana, batch(right, "forbidden"))
			})
		}
	}
}
