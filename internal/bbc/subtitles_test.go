package bbc

import "testing"

func TestTTMLToSRT(t *testing.T) {
	ttml := `<?xml version="1.0" encoding="utf-8"?>
<tt xmlns="http://www.w3.org/ns/ttml" xmlns:ttp="http://www.w3.org/ns/ttml#parameter" ttp:frameRate="25">
  <body>
    <div>
      <p begin="00:00:05.000" end="00:00:08.000">Hello, world!</p>
      <p begin="00:00:10.000" end="00:00:13.500">Second subtitle line.</p>
    </div>
  </body>
</tt>`

	srt, err := TTMLToSRT([]byte(ttml))
	if err != nil {
		t.Fatalf("TTMLToSRT: %v", err)
	}

	expected := "1\n00:00:05,000 --> 00:00:08,000\nHello, world!\n\n2\n00:00:10,000 --> 00:00:13,500\nSecond subtitle line.\n\n"
	if string(srt) != expected {
		t.Errorf("SRT output:\n%s\nwant:\n%s", srt, expected)
	}
}

func TestTTMLToSRTFrameBased(t *testing.T) {
	ttml := `<?xml version="1.0"?>
<tt xmlns="http://www.w3.org/ns/ttml">
  <body>
    <div>
      <p begin="00:01:30:12" end="00:01:33:00">Frame-based timing.</p>
    </div>
  </body>
</tt>`

	srt, err := TTMLToSRT([]byte(ttml))
	if err != nil {
		t.Fatalf("TTMLToSRT: %v", err)
	}

	// 12 frames at 25fps = 480ms
	expected := "1\n00:01:30,480 --> 00:01:33,000\nFrame-based timing.\n\n"
	if string(srt) != expected {
		t.Errorf("SRT output:\n%s\nwant:\n%s", srt, expected)
	}
}

func TestTTMLToSRTEmpty(t *testing.T) {
	ttml := `<?xml version="1.0"?><tt xmlns="http://www.w3.org/ns/ttml"><body><div></div></body></tt>`
	srt, err := TTMLToSRT([]byte(ttml))
	if err != nil {
		t.Fatalf("TTMLToSRT: %v", err)
	}
	if len(srt) != 0 {
		t.Errorf("expected empty SRT, got %q", srt)
	}
}

// TestToSRTTime_BarePeriod exercises GitHub issue #41: BBC's TTML
// drops the .000 fractional part when milliseconds are exactly zero
// ("00:00:01" instead of "00:00:01.000"). Strict SRT parsers reject
// the bare form. toSRTTime must canonicalise every input to the
// HH:MM:SS,mmm shape so the output is always valid.
func TestToSRTTime_BarePeriod(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"00:00:00", "00:00:00,000"},
		{"00:00:01", "00:00:01,000"},
		{"01:23:45", "01:23:45,000"},
		{"00:00:01.000", "00:00:01,000"},
		{"00:00:01.500", "00:00:01,500"},
		{"00:00:01.5", "00:00:01,500"},       // pad short
		{"00:00:01.50", "00:00:01,500"},      // pad short
		{"00:00:01.1234", "00:00:01,123"},    // truncate long
		{"  00:00:42.250  ", "00:00:42,250"}, // trims whitespace
		{"00:01:30:12", "00:01:30,480"},      // frame-based (25fps)
		{"00:01:33:00", "00:01:33,000"},      // frame-based zero
	}
	for _, tc := range cases {
		got := toSRTTime(tc.in)
		if got != tc.want {
			t.Errorf("toSRTTime(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestTTMLToSRT_ZeroMillisAreCanonicalised exercises the end-to-end
// path: TTML cue ends with a bare HH:MM:SS (no .000) and the SRT
// emitter still produces a strict HH:MM:SS,000. Regression anchor
// for GitHub issue #41.
func TestTTMLToSRT_ZeroMillisAreCanonicalised(t *testing.T) {
	ttml := `<?xml version="1.0"?>
<tt xmlns="http://www.w3.org/ns/ttml">
  <body>
    <div>
      <p begin="00:00:00" end="00:00:02">First cue.</p>
      <p begin="00:00:02" end="00:00:04.500">Second cue.</p>
    </div>
  </body>
</tt>`
	srt, err := TTMLToSRT([]byte(ttml))
	if err != nil {
		t.Fatalf("TTMLToSRT: %v", err)
	}
	expected := "1\n00:00:00,000 --> 00:00:02,000\nFirst cue.\n\n2\n00:00:02,000 --> 00:00:04,500\nSecond cue.\n\n"
	if string(srt) != expected {
		t.Errorf("SRT output:\n%q\nwant:\n%q", srt, expected)
	}
}
