package llm

import (
	"encoding/json"
	"testing"

	"opensight/internal/domain"
)

func mustID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	return id
}

func matchInputWith(t *testing.T, names ...string) (MatchInput, domain.ID) {
	t.Helper()
	cid := mustID(t)
	return MatchInput{
		Names:       names,
		Competitors: []MatchCandidate{{ID: cid, Name: "Atlas Dental", Aliases: []string{"atlas"}}},
	}, cid
}

func TestDecodeMatchOutputHappyPath(t *testing.T) {
	in, cid := matchInputWith(t, "Atlas Dental Clinic", "Unknown Co")
	raw := json.RawMessage(`{"matches":[{"index":0,"competitor_id":"` + cid.String() + `"},{"index":1,"competitor_id":null}]}`)

	ids, warnings, err := DecodeMatchOutput(raw, in)
	if err != nil {
		t.Fatalf("DecodeMatchOutput: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if ids[0] != cid {
		t.Errorf("ids[0] = %v, want %v", ids[0], cid)
	}
	if ids[1] != (domain.ID{}) {
		t.Errorf("ids[1] = %v, want nil", ids[1])
	}
}

func TestDecodeMatchOutputOutOfRangeIndex(t *testing.T) {
	in, cid := matchInputWith(t, "Only One")
	raw := json.RawMessage(`{"matches":[{"index":5,"competitor_id":"` + cid.String() + `"}]}`)

	ids, warnings, err := DecodeMatchOutput(raw, in)
	if err != nil {
		t.Fatalf("DecodeMatchOutput: %v", err)
	}
	if ids[0] != (domain.ID{}) {
		t.Errorf("ids[0] = %v, want nil (out-of-range entry ignored)", ids[0])
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want 1", warnings)
	}
}

func TestDecodeMatchOutputDuplicateIndex(t *testing.T) {
	in, cid := matchInputWith(t, "Atlas Dental Clinic")
	// First entry matches; the duplicate (null) must be ignored, not overwrite it.
	raw := json.RawMessage(`{"matches":[{"index":0,"competitor_id":"` + cid.String() + `"},{"index":0,"competitor_id":null}]}`)

	ids, warnings, err := DecodeMatchOutput(raw, in)
	if err != nil {
		t.Fatalf("DecodeMatchOutput: %v", err)
	}
	if ids[0] != cid {
		t.Errorf("ids[0] = %v, want %v (first entry wins)", ids[0], cid)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want 1 duplicate warning", warnings)
	}
}

func TestDecodeMatchOutputUnknownID(t *testing.T) {
	in, _ := matchInputWith(t, "Atlas Dental Clinic")
	other := mustID(t)
	raw := json.RawMessage(`{"matches":[{"index":0,"competitor_id":"` + other.String() + `"}]}`)

	ids, warnings, err := DecodeMatchOutput(raw, in)
	if err != nil {
		t.Fatalf("DecodeMatchOutput: %v", err)
	}
	if ids[0] != (domain.ID{}) {
		t.Errorf("ids[0] = %v, want nil (id not in candidate list)", ids[0])
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want 1 unknown-id warning", warnings)
	}
}

func TestDecodeMatchOutputAllNull(t *testing.T) {
	in, _ := matchInputWith(t, "A", "B")
	raw := json.RawMessage(`{"matches":[{"index":0,"competitor_id":null},{"index":1,"competitor_id":null}]}`)

	ids, warnings, err := DecodeMatchOutput(raw, in)
	if err != nil {
		t.Fatalf("DecodeMatchOutput: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	for i, id := range ids {
		if id != (domain.ID{}) {
			t.Errorf("ids[%d] = %v, want nil", i, id)
		}
	}
}

func TestDecodeMatchOutputEmptyNames(t *testing.T) {
	in := MatchInput{Names: nil, Competitors: nil}
	ids, _, err := DecodeMatchOutput(json.RawMessage(`{"matches":[]}`), in)
	if err != nil {
		t.Fatalf("DecodeMatchOutput: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v, want empty", ids)
	}
}

func TestStubMatchRunnerMatchesNothing(t *testing.T) {
	in, _ := matchInputWith(t, "A", "B", "C")
	runner, err := NewStubMatchRunner()
	if err != nil {
		t.Fatalf("NewStubMatchRunner: %v", err)
	}
	res, err := runner.RunMatch(nil, in) //nolint:staticcheck // stub ignores ctx
	if err != nil {
		t.Fatalf("RunMatch: %v", err)
	}
	ids, _, err := DecodeMatchOutput(res.RawJSON, in)
	if err != nil {
		t.Fatalf("DecodeMatchOutput: %v", err)
	}
	for i, id := range ids {
		if id != (domain.ID{}) {
			t.Errorf("stub ids[%d] = %v, want nil (stub matches nothing)", i, id)
		}
	}
}
