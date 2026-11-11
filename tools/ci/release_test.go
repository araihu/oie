package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReleaseIdentityRejectsUnscopedBuildInputs(t *testing.T) {
	valid := releaseIdentity{Version: "v0.0.1-rc.0", Revision: strings.Repeat("a", 40), PackagingRevision: strings.Repeat("b", 40), SourceArchiveSHA256: strings.Repeat("c", 64)}
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeReleaseIdentity(string(encoded)); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"version", "revision", "packagingRevision", "sourceArchiveSHA256"} {
		var values map[string]string
		if err := json.Unmarshal(encoded, &values); err != nil {
			t.Fatal(err)
		}
		values[field] = "../../unverified"
		data, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeReleaseIdentity(string(data)); err == nil {
			t.Fatalf("accepted unverified %s", field)
		}
	}
}
