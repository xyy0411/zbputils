package job

import "testing"

func TestParseGroupIDs(t *testing.T) {
	got, err := parseGroupIDs("10001, 10002，10003")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 10001 || got[1] != 10002 || got[2] != 10003 {
		t.Fatalf("parseGroupIDs() = %#v", got)
	}
}

func TestParseGroupIDsRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{"", "10001,0", "10001,10001", "abc"} {
		t.Run(input, func(t *testing.T) {
			if _, err := parseGroupIDs(input); err == nil {
				t.Fatalf("parseGroupIDs(%q) succeeded", input)
			}
		})
	}
}
