package textsender

import "testing"

func TestParsePort(t *testing.T) {
	tests := []struct {
		in      string
		want    uint16
		wantErr bool
	}{
		{"", 1, false},
		{"1", 1, false},
		{"65535", 65535, false},
		{"0", 0, true},
		{"65536", 0, true},
		{"-1", 0, true},
		{"http", 0, true},
	}
	for _, tt := range tests {
		got, err := ParsePort(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParsePort(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("ParsePort(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
