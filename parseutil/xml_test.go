package parseutil

import (
	"encoding/xml"
	"testing"
)

type testURLSet struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

func TestDecodeXMLMatchesUnmarshalOnValidInput(t *testing.T) {
	const doc = `<urlset><url><loc>https://e.com/a</loc></url><url><loc>https://e.com/b</loc></url></urlset>`
	var strict, lenient testURLSet
	if err := xml.Unmarshal([]byte(doc), &strict); err != nil {
		t.Fatalf("xml.Unmarshal: %v", err)
	}
	if err := DecodeXML([]byte(doc), &lenient); err != nil {
		t.Fatalf("DecodeXML: %v", err)
	}
	if len(lenient.URLs) != len(strict.URLs) || len(lenient.URLs) != 2 {
		t.Fatalf("got %d urls, want %d", len(lenient.URLs), len(strict.URLs))
	}
	for i := range strict.URLs {
		if lenient.URLs[i].Loc != strict.URLs[i].Loc {
			t.Errorf("url %d = %q, want %q", i, lenient.URLs[i].Loc, strict.URLs[i].Loc)
		}
	}
}

// The regression: a bare & in a title made strict parsing discard every entry,
// including the ones before and after it.
func TestDecodeXMLToleratesUnescapedAmpersand(t *testing.T) {
	const doc = `<urlset>
<url><loc>https://e.com/a</loc><video:title>Sucks, Fucks & Explodes</video:title></url>
<url><loc>https://e.com/b</loc><video:title>Tom &A Jerry</video:title></url>
<url><loc>https://e.com/c</loc></url>
</urlset>`
	if err := xml.Unmarshal([]byte(doc), new(testURLSet)); err == nil {
		t.Fatal("xml.Unmarshal accepted the bare ampersand; this test no longer guards anything")
	}

	var got testURLSet
	if err := DecodeXML([]byte(doc), &got); err != nil {
		t.Fatalf("DecodeXML: %v", err)
	}
	want := []string{"https://e.com/a", "https://e.com/b", "https://e.com/c"}
	if len(got.URLs) != len(want) {
		t.Fatalf("got %d urls, want %d — entries after the bad one were dropped", len(got.URLs), len(want))
	}
	for i, w := range want {
		if got.URLs[i].Loc != w {
			t.Errorf("url %d = %q, want %q", i, got.URLs[i].Loc, w)
		}
	}
}

// Escaped entities keep their meaning; leniency is not a licence to mangle.
func TestDecodeXMLKeepsEscapedEntities(t *testing.T) {
	var got testURLSet
	if err := DecodeXML([]byte(`<urlset><url><loc>https://e.com/?a=1&amp;b=2</loc></url></urlset>`), &got); err != nil {
		t.Fatalf("DecodeXML: %v", err)
	}
	if len(got.URLs) != 1 || got.URLs[0].Loc != "https://e.com/?a=1&b=2" {
		t.Fatalf("got %+v, want the decoded &", got.URLs)
	}
}

func TestDecodeXMLStillErrorsOnGarbage(t *testing.T) {
	if err := DecodeXML([]byte("this is not xml at all"), new(testURLSet)); err == nil {
		t.Error("want an error for input that is not XML")
	}
}
