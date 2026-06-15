package cage

import "testing"

func TestTrimTrailingNewlinesTrimsInPlaceAndZeroesRemovedBytes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "lf", in: "secret\n", want: "secret"},
		{name: "crlf", in: "secret\r\n", want: "secret"},
		{name: "unchanged", in: "secret", want: "secret"},
		{name: "only newlines", in: "\r\n", want: ""},
		{name: "empty", in: "", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.in)
			got := trimTrailingNewlines(data)
			if string(got) != tc.want {
				t.Fatalf("trimTrailingNewlines(%q) = %q, want %q", tc.in, string(got), tc.want)
			}
			if len(got) > 0 && &got[0] != &data[0] {
				t.Fatal("trimTrailingNewlines copied input")
			}
			if !allZero(data[len(got):]) {
				t.Fatalf("trimmed bytes were not zeroed: %#v", data[len(got):])
			}
		})
	}
}
