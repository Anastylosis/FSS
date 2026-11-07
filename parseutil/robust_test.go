package parseutil

import (
	"testing"
)

func TestParseDurationColonRejectsGarbage(t *testing.T) {
	cases := map[string]int{
		"1:9223372036854775807": 0,
		"1:xx:30":               0,
		"1:500":                 0,
		"1:75":                  0,
		"90:00":                 5400,
		"1:02:03":               3723,
		"12:34":                 754,
		"-1:30":                 0,
	}
	for in, want := range cases {
		if got := ParseDurationColon(in); got != want {
			t.Errorf("ParseDurationColon(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseDurationISOFractionalSeconds(t *testing.T) {
	cases := map[string]int{
		"PT25M30.5S":   1531,
		"PT25M30.4S":   1530,
		"PT1H0M0.000S": 3600,
		"PT0.6S":       1,
		"PTinfS":       0,
		"PTNaNS":       0,
		"PT-5S":        0,
	}
	for in, want := range cases {
		if got := ParseDurationISO(in); got != want {
			t.Errorf("ParseDurationISO(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestDecodeXMLLegacyCharsets(t *testing.T) {
	for _, enc := range []string{"ISO-8859-1", "windows-1252", "iso-8859-1"} {
		body := []byte("<?xml version=\"1.0\" encoding=\"" + enc + "\"?><r><title>caf\xe9</title></r>")
		var v struct {
			Title string `xml:"title"`
		}
		if err := DecodeXML(body, &v); err != nil {
			t.Fatalf("%s: %v", enc, err)
		}
		if v.Title != "café" {
			t.Errorf("%s: title = %q", enc, v.Title)
		}
	}
	var v struct{}
	if err := DecodeXML([]byte(`<?xml version="1.0" encoding="x-nope"?><r/>`), &v); err == nil {
		t.Error("unknown charset should still error")
	}
}

func TestOpenGraphAttributeForms(t *testing.T) {
	body := []byte(`
<meta property="og:title" data-content="WRONG" content="RIGHT">
<meta name="og:image" content='img.jpg'>
<meta content=bare.mp4 property=og:video>
<meta class="x" property="og:url" content="u">
<META PROPERTY="OG:type" CONTENT="video">`)
	got := OpenGraph(body)
	want := map[string]string{
		"og:title": "RIGHT", "og:image": "img.jpg", "og:video": "bare.mp4",
		"og:url": "u", "OG:type": "video",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}

func TestExtractVideoObjectShapes(t *testing.T) {
	wrap := func(attr, json string) []byte {
		return []byte("<script " + attr + ">" + json + "</script>")
	}
	const dq = `type="application/ld+json"`
	cases := []struct {
		name string
		body []byte
		want string
	}{
		{"graph", wrap(dq, `{"@context":"x","@graph":[{"@type":"WebPage"},{"@type":"VideoObject","name":"G"}]}`), "G"},
		{"array type", wrap(dq, `{"@type":["VideoObject","Movie"],"name":"A"}`), "A"},
		{"top-level array", wrap(dq, `[{"@type":"WebSite"},{"@type":"VideoObject","name":"T"}]`), "T"},
		{"numeric duration", wrap(dq, `{"@type":"VideoObject","name":"N","duration":1234}`), "N"},
		{"array name", wrap(dq, `{"@type":"VideoObject","name":["AN","x"]}`), "AN"},
		{"itemlist direct", wrap(dq, `{"@type":"ItemList","itemListElement":["http://u",{"@type":"VideoObject","name":"D"}]}`), "D"},
		{"itemlist listitem", wrap(dq, `{"@type":"ItemList","itemListElement":[{"@type":"ListItem","item":{"@type":"VideoObject","name":"L"}}]}`), "L"},
		{"raw newline", wrap(dq, "{\"@type\":\"VideoObject\",\"name\":\"NL\",\"description\":\"a\nb\"}"), "NL"},
		{"single quoted", wrap(`type='application/ld+json'`, `{"@type":"VideoObject","name":"S"}`), "S"},
		{"uppercase", wrap(`TYPE="APPLICATION/LD+JSON"`, `{"@type":"VideoObject","name":"U"}`), "U"},
	}
	for _, c := range cases {
		vo := ExtractVideoObject(c.body)
		if vo == nil || vo.Name != c.want {
			t.Errorf("%s: got %+v, want name %q", c.name, vo, c.want)
		}
	}
	if vo := ExtractVideoObject(wrap(dq, `{"@type":"VideoObject","duration":1234}`)); vo == nil || vo.Duration != "1234" {
		t.Errorf("numeric duration = %+v", vo)
	}
	if vo := ExtractVideoObject(wrap(dq, "{\"@type\":\"VideoObject\",\"description\":\"a\\nb\\\"c\nd\"}")); vo == nil || vo.Description != "a\nb\"c\nd" {
		t.Errorf("newline description = %+v", vo)
	}
	if n := len(ExtractVideoObjects(wrap(dq, `{"@graph":[{"@type":"VideoObject"},{"@type":"VideoObject"}]}`))); n != 2 {
		t.Errorf("graph count = %d", n)
	}
}
