package domain

import (
	"testing"
	"time"
)

func TestCurrentRevisionCausalityConcurrencyAndImportOrder(t *testing.T) {
	base := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	entity := ID("019a5df0-0000-7000-8000-000000000000")
	a := Revision{ID: "019a5df0-0000-7000-8000-000000000001", EntityID: entity, CreatedAt: base}
	b := Revision{ID: "019a5df0-0000-7000-8000-000000000002", EntityID: entity, Parents: []ID{a.ID}, CreatedAt: base.Add(-time.Hour)}
	c := Revision{ID: "019a5df0-0000-7000-8000-000000000003", EntityID: entity, Parents: []ID{a.ID}, CreatedAt: base.Add(-time.Hour)}
	for _, history := range [][]Revision{{a, b}, {b, a}} {
		got, err := CurrentRevision(history)
		if err != nil || got.ID != b.ID {
			t.Fatalf("descendant must win despite clock skew: %+v, %v", got, err)
		}
	}
	for _, history := range [][]Revision{{a, b, c}, {c, a, b}, {b, c, a}} {
		got, err := CurrentRevision(history)
		if err != nil || got.ID != c.ID {
			t.Fatalf("concurrent equal-time tie must converge by UUID: %+v, %v", got, err)
		}
	}
	b.CreatedAt = base.Add(time.Hour)
	got, err := CurrentRevision([]Revision{c, b, a})
	if err != nil || got.ID != b.ID {
		t.Fatalf("newer concurrent head should win: %+v %v", got, err)
	}
	a.Parents = []ID{b.ID}
	if _, err := CurrentRevision([]Revision{a, b}); err == nil {
		t.Fatal("cycle accepted")
	}
}

func TestIDRequiresCanonicalUUIDv7(t *testing.T) {
	for _, invalid := range []string{"", "alice", "019A5DF0-0000-7000-8000-000000000001", "019a5df0-0000-4000-8000-000000000001"} {
		if _, err := ParseID(invalid); err == nil {
			t.Fatalf("accepted invalid id %q", invalid)
		}
	}
}
