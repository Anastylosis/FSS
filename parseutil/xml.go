package parseutil

import (
	"bytes"
	"encoding/xml"
)

// DecodeXML unmarshals XML into v, tolerating the malformed markup that real
// sitemaps and feeds routinely carry — most often an unescaped `&` in a title.
// Strict encoding/xml rejects such a document outright, so one bare ampersand
// anywhere in a sitemap costs the whole catalogue rather than the single entry
// containing it.
//
// A document that parses strictly parses here identically; the leniency only
// affects input xml.Unmarshal would have refused.
func DecodeXML(body []byte, v any) error {
	d := xml.NewDecoder(bytes.NewReader(body))
	d.Strict = false
	return d.Decode(v)
}
