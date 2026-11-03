package vipsexvault

import "testing"

func TestListPath(t *testing.T) {
	cases := map[string]string{
		"https://vipsexvault.com/":                                    "/videos.en.html",
		"https://vipsexvault.com/channels/fucked-in-traffic.en.html":  "/channels/fucked-in-traffic.en.html",
		"https://www.vipsexvault.com/models/chelsy-sun.en.html?x=1":   "/models/chelsy-sun.en.html",
		"https://vipsexvault.com/videos.en.html?order=-recent&page=2": "/videos.en.html",
	}
	for in, want := range cases {
		if got := listPath(in); got != want {
			t.Errorf("listPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseListingCardFields(t *testing.T) {
	first := parseListing(readFixture(t, "listing.html"))[0]
	if first.studio != "Fucked In Traffic" || first.date != "Oct 02, 2023" || len(first.performers) != 1 {
		t.Errorf("card = %+v", first)
	}
}
