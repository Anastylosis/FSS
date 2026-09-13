package parseutil

import (
	"encoding/json"
	"regexp"
	"strings"
)

// VideoObject holds the common fields from a schema.org VideoObject
// embedded in a page's JSON-LD script block.
type VideoObject struct {
	URL           string   `json:"url"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	ThumbnailURL  string   `json:"thumbnailUrl"`
	ContentURL    string   `json:"contentUrl"`
	Duration      string   `json:"duration"`
	UploadDate    string   `json:"uploadDate"`
	DatePublished string   `json:"datePublished"`
	Actors        []string `json:"-"`
	Director      string   `json:"-"`
	Keywords      string   `json:"keywords"`
	// Genres is the schema.org genre field, which publishers use for the tag
	// taxonomy. Kept as a list because that is how it is published; Keywords
	// stays a string for compatibility and joins a published list with ", ".
	Genres       []string `json:"-"`
	PartOfSeries string   `json:"-"`
}

type rawVideoObject struct {
	Type          string          `json:"@type"`
	URL           string          `json:"url"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	ThumbnailURL  flexString      `json:"thumbnailUrl"`
	ContentURL    string          `json:"contentUrl"`
	Duration      string          `json:"duration"`
	UploadDate    string          `json:"uploadDate"`
	DatePublished string          `json:"datePublished"`
	Actor         json.RawMessage `json:"actor"`
	Director      json.RawMessage `json:"director"`
	Keywords      flexStrings     `json:"keywords"`
	Genre         flexStrings     `json:"genre"`
	PartOfSeries  *struct {
		Name string `json:"name"`
	} `json:"partOfSeries"`
}

// flexString accepts a JSON string or an array of them, keeping the first.
// schema.org declares thumbnailUrl as an ImageObject *or* a URL, and publishers
// split on it: most emit a bare string, some (bananafever) an array. Declaring
// it as a plain string made json.Unmarshal fail on the whole block, so the
// VideoObject was silently skipped and the page read as having none at all.
// flexStrings accepts a JSON string, an array of strings, or an array of
// objects carrying a "name". schema.org allows keywords and genre to be either
// a comma-separated string or a list, and publishers split on it — EnjoyX emits
// arrays. Declared as a plain string, one array made json.Unmarshal fail on the
// whole block, so the VideoObject was dropped and the page read as having none:
// the same failure flexString exists to prevent, one field over.
type flexStrings []string

func (f *flexStrings) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*f = splitList(one)
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err == nil {
		*f = trimAll(many)
		return nil
	}
	var objects []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(b, &objects); err == nil {
		out := make([]string, 0, len(objects))
		for _, o := range objects {
			if name := strings.TrimSpace(o.Name); name != "" {
				out = append(out, name)
			}
		}
		*f = out
		return nil
	}
	// Anything else is ignored rather than failing the decode: one odd field
	// must not cost the caller the whole VideoObject.
	*f = nil
	return nil
}

// splitList turns a comma-separated keyword string into its parts.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*f = flexString(one)
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err == nil {
		if len(many) > 0 {
			*f = flexString(many[0])
		}
		return nil
	}
	// Anything else — an ImageObject, say — leaves the field empty rather than
	// failing the block it belongs to.
	*f = ""
	return nil
}

type rawItemList struct {
	Type     string `json:"@type"`
	Elements []struct {
		Item rawVideoObject `json:"item"`
	} `json:"itemListElement"`
}

var jsonLDBlockRe = regexp.MustCompile(`(?s)<script[^>]+type="application/ld\+json"[^>]*>(.*?)</script>`)

// ExtractVideoObject finds the first VideoObject in the page's JSON-LD
// blocks. Returns nil if none is found. It handles both bare VideoObject
// blocks and ItemList wrappers (returning the first item). Actor fields
// are parsed flexibly: arrays of strings, arrays of {"name":"…"} objects,
// or a single string all work.
func ExtractVideoObject(body []byte) *VideoObject {
	vos := extractVideoObjects(body, true)
	if len(vos) == 0 {
		return nil
	}
	return &vos[0]
}

// ExtractVideoObjects returns all VideoObject entries found in the
// page's JSON-LD blocks, including those wrapped in an ItemList.
func ExtractVideoObjects(body []byte) []VideoObject {
	return extractVideoObjects(body, false)
}

func extractVideoObjects(body []byte, firstOnly bool) []VideoObject {
	var result []VideoObject
	for _, m := range jsonLDBlockRe.FindAllSubmatch(body, -1) {
		raw := m[1]

		var probe struct {
			Type string `json:"@type"`
		}
		if json.Unmarshal(raw, &probe) != nil {
			continue
		}

		switch probe.Type {
		case "VideoObject":
			var rvo rawVideoObject
			if json.Unmarshal(raw, &rvo) != nil {
				continue
			}
			result = append(result, convertRaw(rvo))
			if firstOnly {
				return result
			}

		case "ItemList":
			var il rawItemList
			if json.Unmarshal(raw, &il) != nil {
				continue
			}
			for _, elem := range il.Elements {
				if elem.Item.Type == "VideoObject" {
					result = append(result, convertRaw(elem.Item))
					if firstOnly {
						return result
					}
				}
			}
		}
	}
	return result
}

func convertRaw(rvo rawVideoObject) VideoObject {
	vo := VideoObject{
		URL:           rvo.URL,
		Name:          rvo.Name,
		Description:   rvo.Description,
		ThumbnailURL:  string(rvo.ThumbnailURL),
		ContentURL:    rvo.ContentURL,
		Duration:      rvo.Duration,
		UploadDate:    rvo.UploadDate,
		DatePublished: rvo.DatePublished,
		Keywords:      strings.Join(rvo.Keywords, ", "),
		Genres:        rvo.Genre,
	}
	if rvo.PartOfSeries != nil {
		vo.PartOfSeries = rvo.PartOfSeries.Name
	}
	vo.Actors = parseFlexActors(rvo.Actor)
	vo.Director = parseFlexPerson(rvo.Director)
	return vo
}

// parseFlexActors handles three JSON shapes for the actor field:
//   - array of strings: ["Alice","Bob"]
//   - array of objects: [{"name":"Alice"},{"name":"Bob"}]
//   - single string:    "Alice"
//   - single object:    {"name":"Alice"}
func parseFlexActors(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil
	}

	if raw[0] == '[' {
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) != nil {
			return nil
		}
		var names []string
		for _, elem := range arr {
			elem = []byte(strings.TrimSpace(string(elem)))
			if len(elem) == 0 {
				continue
			}
			switch elem[0] {
			case '"':
				var s string
				if json.Unmarshal(elem, &s) == nil {
					if n := strings.TrimSpace(s); n != "" {
						names = append(names, n)
					}
				}
			case '{':
				var obj struct {
					Name string `json:"name"`
				}
				if json.Unmarshal(elem, &obj) == nil {
					if n := strings.TrimSpace(obj.Name); n != "" {
						names = append(names, n)
					}
				}
			}
		}
		return names
	}

	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if n := strings.TrimSpace(s); n != "" {
				return []string{n}
			}
		}
	}

	if raw[0] == '{' {
		var obj struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &obj) == nil {
			if n := strings.TrimSpace(obj.Name); n != "" {
				return []string{n}
			}
		}
	}

	return nil
}

func parseFlexPerson(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var obj struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return strings.TrimSpace(obj.Name)
	}
	return ""
}
