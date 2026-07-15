package manual

import "testing"

// Test_ToOutputType pins the 8-row input→output type table (a pure
// mapping with real decision surface) plus the Go-only unknown-type row: an
// unknown value maps to itself and reports known=false so the handler warns rather than
// failing the run.
func Test_ToOutputType(t *testing.T) {
	cases := []struct {
		in       string
		want     string
		wantKnwn bool
	}{
		{typeString, typeString, true},
		{typeInteger, typeInteger, true},
		{typeBoolean, typeBoolean, true},
		{typeCode, typeCode, true},
		{typeFile, typeString, true},
		{typeList, typeCode, true},
		{typeSingleSelect, typeString, true},
		{typeMultiSelect, typeCode, true},
		{"SOMETHING_NEW", "SOMETHING_NEW", false},
	}
	for _, c := range cases {
		got, known := toOutputType(c.in)
		if got != c.want || known != c.wantKnwn {
			t.Errorf("toOutputType(%q) = (%q, %v), want (%q, %v)", c.in, got, known, c.want, c.wantKnwn)
		}
	}
}
