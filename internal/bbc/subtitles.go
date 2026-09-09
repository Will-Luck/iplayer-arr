package bbc

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type ttDoc struct {
	XMLName xml.Name `xml:"tt"`
	Body    struct {
		Divs []struct {
			Paragraphs []struct {
				Begin string `xml:"begin,attr"`
				End   string `xml:"end,attr"`
				Text  string `xml:",chardata"`
				Spans []struct {
					Text string `xml:",chardata"`
				} `xml:"span"`
			} `xml:"p"`
		} `xml:"div"`
	} `xml:"body"`
}

// srtCue is one emitted SRT block: a timing pair and one or more text lines.
type srtCue struct {
	begin, end string
	lines      []string
}

// TTMLToSRT converts BBC TTML subtitle data to SRT format.
// BBC TTML may use period-based timing (HH:MM:SS.mmm) or frame-based timing
// (HH:MM:SS:FF at 25fps). Text may be wrapped in <span> elements.
//
// The BBC splits one on-screen caption into several consecutive <p>
// elements that share the same begin/end, each placed in its own
// positioned region (document order is top row to bottom row). SRT
// carries no positioning, so emitting one cue per <p> yields
// consecutive cues with identical timestamps and players stack them
// by their own rule, usually newest on top, which reverses the lines
// (GitHub issue #53). A run of adjacent same-timing paragraphs is
// therefore merged into a single multi-line cue in document order.
func TTMLToSRT(ttml []byte) ([]byte, error) {
	// Strip XML default namespaces -- Go's encoding/xml silently fails to match
	// child elements when a default namespace propagates from the root.
	cleaned := reXMLNS.ReplaceAll(ttml, nil)

	var doc ttDoc
	if err := xml.Unmarshal(cleaned, &doc); err != nil {
		return nil, fmt.Errorf("parse TTML: %w", err)
	}

	var cues []srtCue

	for _, div := range doc.Body.Divs {
		for _, p := range div.Paragraphs {
			text := p.Text
			if text == "" {
				var parts []string
				for _, s := range p.Spans {
					if t := strings.TrimSpace(s.Text); t != "" {
						parts = append(parts, t)
					}
				}
				text = strings.Join(parts, " ")
			}
			text = strings.TrimSpace(stripTags(text))
			if text == "" {
				continue
			}

			begin := toSRTTime(p.Begin)
			end := toSRTTime(p.End)

			// Merge only with the immediately preceding cue: the split
			// rows of one caption are always adjacent in the document.
			if n := len(cues); n > 0 && cues[n-1].begin == begin && cues[n-1].end == end {
				cues[n-1].lines = append(cues[n-1].lines, text)
				continue
			}
			cues = append(cues, srtCue{begin: begin, end: end, lines: []string{text}})
		}
	}

	var b strings.Builder
	for i, c := range cues {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, c.begin, c.end, strings.Join(c.lines, "\n"))
	}

	return []byte(b.String()), nil
}

var (
	reHTMLTag = regexp.MustCompile(`<[^>]+>`)
	reXMLNS   = regexp.MustCompile(`\s+xmlns(?::\w+)?="[^"]*"`)
)

func stripTags(s string) string {
	return reHTMLTag.ReplaceAllString(s, "")
}

// toSRTTime converts a TTML timestamp to canonical SRT format
// (HH:MM:SS,mmm). TTML may use any of:
//
//   - period-based HH:MM:SS.mmm
//   - frame-based  HH:MM:SS:FF (assumed 25 fps)
//   - bare         HH:MM:SS (BBC's TTML omits the fractional part when
//     it is exactly zero; strict SRT parsers reject this form)
//
// Output is always exactly HH:MM:SS,mmm so downstream players that
// validate the format strictly (VLC's stricter mode, some embedded
// renderers) accept every cue. GitHub issue #41.
func toSRTTime(t string) string {
	t = strings.TrimSpace(t)
	if strings.Count(t, ":") == 3 {
		// frame-based: HH:MM:SS:FF -> convert FF to milliseconds (assume 25fps)
		parts := strings.Split(t, ":")
		if len(parts) == 4 {
			frames, _ := strconv.Atoi(parts[3])
			ms := (frames * 1000) / 25
			return fmt.Sprintf("%s:%s:%s,%03d", parts[0], parts[1], parts[2], ms)
		}
	}
	// period-based: HH:MM:SS[.mmm] -> HH:MM:SS,mmm. Missing fraction
	// defaults to zero; short fractions are right-padded; long
	// fractions are truncated. This is the strict-SRT contract.
	parts := strings.SplitN(t, ".", 2)
	ms := "000"
	if len(parts) == 2 {
		frac := parts[1]
		if len(frac) > 3 {
			frac = frac[:3]
		}
		for len(frac) < 3 {
			frac += "0"
		}
		ms = frac
	}
	return parts[0] + "," + ms
}
