package providers

import "testing"

func TestNormalizeScale(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		max     float64
		want    int
		wantErr bool
	}{
		{"imdb 10-scale", 7.9, 10, 79, false},
		{"hardcover 5-scale", 4.28, 5, 86, false},
		{"igdb float", 91.234, 100, 91, false},
		{"floor", 0, 10, 0, false},
		{"ceiling", 100, 100, 100, false},
		{"rounds half up", 4.35, 5, 87, false},
		{"negative value", -1, 10, 0, true},
		{"value over max", 11, 10, 0, true},
		{"zero max", 5, 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeScale(tt.value, tt.max)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeScale(%v, %v) error = %v, wantErr %v", tt.value, tt.max, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("NormalizeScale(%v, %v) = %d, want %d", tt.value, tt.max, got, tt.want)
			}
		})
	}
}

func TestParseDisplay(t *testing.T) {
	tests := []struct {
		display string
		want    int
		wantErr bool
	}{
		{"7.9/10", 79, false},
		{"85%", 85, false},
		{"74/100", 74, false},
		{"4.2/5", 84, false},
		{" 8.7/10 ", 87, false},
		{"12/10", 0, true},
		{"N/A/10", 0, true},
		{"eighty", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.display, func(t *testing.T) {
			got, err := ParseDisplay(tt.display)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseDisplay(%q) error = %v, wantErr %v", tt.display, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseDisplay(%q) = %d, want %d", tt.display, got, tt.want)
			}
		})
	}
}
