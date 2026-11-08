package elxupdateutil

import "testing"

func TestParseListingLinklessCards(t *testing.T) {
	items := parseListing(readFixture(t, "listing_linkless.html"), true)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	it := items[0]
	if it.title != "TG-202 The Breakfast Club Scene 2" || it.date != "11/20/2026" {
		t.Errorf("title/date = %q / %q", it.title, it.date)
	}
	if len(it.performers) != 2 || it.performers[0] != "Chloe" || it.performers[1] != "Mia" {
		t.Errorf("performers = %v", it.performers)
	}
	if it.thumbnail != "/tour/content//contentthumbs/99/06/39906-tour-4x.jpg" {
		t.Errorf("thumbnail = %q", it.thumbnail)
	}
	if items[0].id == items[1].id {
		t.Error("ids collide")
	}
	if got := parseListing(readFixture(t, "listing_linkless.html"), false); len(got) != 0 {
		t.Errorf("linked-only mode returned %d items", len(got))
	}
}
