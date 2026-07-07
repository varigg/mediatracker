package names

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"The Witcher® 3: Wild Hunt", "the witcher 3 wild hunt"},
		{"The Witcher 3: Wild Hunt – Complete Edition", "the witcher 3 wild hunt"},
		{"Forza Horizon 5 Deluxe Edition", "forza horizon 5"},
		{"HALO: The Master Chief Collection", "halo the master chief collection"},
		{"Wiedźmin 3: Dziki Gon", "wiedmin 3 dziki gon"},
		{"Control Ultimate Edition", "control"},
		{"Persona 5 Royal", "persona 5 royal"},
		{"Fallout 4: Game of the Year Edition", "fallout 4"},
		{"Grounded", "grounded"},
		{"  spaced   out  ", "spaced out"},
		{"™®©", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := Normalize(tt.in); got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSetLookup(t *testing.T) {
	url := "https://example.com/witcher"
	s := NewSet()
	s.Add("The Witcher 3: Wild Hunt – Complete Edition", &url)
	s.Add("Halo Infinite", nil)
	s.Add("", nil) // must not panic or register an empty key

	// Item title matches a catalog entry that carries an edition suffix.
	e, ok := s.Lookup("The Witcher 3: Wild Hunt")
	if !ok {
		t.Fatal("expected Deluxe/Complete Edition catalog entry to match base title")
	}
	if e.URL == nil || *e.URL != url {
		t.Errorf("entry URL = %v, want %s", e.URL, url)
	}

	// First candidate misses, alternative name hits.
	if _, ok := s.Lookup("Nonexistent Game", "HALO INFINITE"); !ok {
		t.Error("expected alternative-name candidate to match")
	}

	if _, ok := s.Lookup("Starfield"); ok {
		t.Error("expected no match for absent title")
	}
}
