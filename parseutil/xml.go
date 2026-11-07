package parseutil

import (
	"bytes"
	"encoding/xml"
	"io"

	"golang.org/x/text/encoding/htmlindex"
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
	d.CharsetReader = charsetReader
	return d.Decode(v)
}

// charsetReader converts a declared non-UTF-8 encoding (ISO-8859-1,
// windows-1252, …) to UTF-8. An unknown label is an error, as before.
func charsetReader(label string, input io.Reader) (io.Reader, error) {
	enc, err := htmlindex.Get(label)
	if err != nil {
		return nil, err
	}
	return enc.NewDecoder().Reader(input), nil
}
