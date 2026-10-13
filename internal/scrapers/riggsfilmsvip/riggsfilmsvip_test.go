package riggsfilmsvip

import "testing"

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://riggsfilms.vip", "https://www.riggsfilms.vip/", "http://riggsfilms.vip/222"} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %q", u)
		}
	}
	for _, u := range []string{"https://riggsfilms.com", "https://riggsfilms.vip.example.com", "https://xriggsfilms.vip"} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %q", u)
		}
	}
}

func TestGenericDescription(t *testing.T) {
	for d, want := range map[string]bool{
		"Riggs Films. Australia's Number 1 Porn Site!":      true,
		"https://www.babepedia.com/Babepedia":               true,
		"Watch Isabelle Deltore & Kayla Lea share one guy.": false,
	} {
		if got := genericDescription(d); got != want {
			t.Errorf("genericDescription(%q) = %v, want %v", d, got, want)
		}
	}
}

func TestIdentity(t *testing.T) {
	s := New()
	if s.ID() != "riggsfilmsvip" {
		t.Errorf("ID = %q", s.ID())
	}
	if p := s.Patterns(); len(p) != 1 || p[0] != "riggsfilms.vip" {
		t.Errorf("Patterns = %v", p)
	}
}
