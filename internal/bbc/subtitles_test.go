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

// TestTTMLToSRT_SameTimingParagraphsMerge exercises GitHub issue #53.
// BBC TTML splits one on-screen caption into several <p> elements
// with identical begin/end, each in its own positioned region
// (document order is top row to bottom row). Emitting one SRT cue
// per <p> produces consecutive cues with the same timestamps, and
// players stack simultaneous cues by their own rule (usually newest
// on top), reversing the lines. The converter must merge a run of
// same-timing paragraphs into one multi-line cue in document order.
// Fixture is a verbatim fragment of Waterloo Road S1E1 (p00c9rpm).
func TestTTMLToSRT_SameTimingParagraphsMerge(t *testing.T) {
	ttml := `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns="http://www.w3.org/ns/ttml" xmlns:tts="http://www.w3.org/ns/ttml#styling">
  <body>
    <div>
      <p xml:id="C110_1" region="R9" style="S2" begin="00:08:23.000" end="00:08:25.320"><span style="S3">Lorna... We'll be seeing him every day.</span></p>
      <p xml:id="C111_1" region="agr_1_18_39_2_after" style="S2" begin="00:08:25.320" end="00:08:29.760"><span style="S4">Just calm down, yeah?</span></p>
      <p xml:id="C111_2" region="R47" style="S2" begin="00:08:25.320" end="00:08:29.760"><span style="S4">But if he's good-looking and single,</span></p>
      <p xml:id="C111_3" region="R9" style="S2" begin="00:08:25.320" end="00:08:29.760"><span style="S4">yes, please!</span></p>
      <p xml:id="C112_1" region="R9" style="S2" begin="00:08:29.760" end="00:08:31.000"><span style="S3">Next cue.</span></p>
    </div>
  </body>
</tt>`
	srt, err := TTMLToSRT([]byte(ttml))
	if err != nil {
		t.Fatalf("TTMLToSRT: %v", err)
	}
	expected := "1\n00:08:23,000 --> 00:08:25,320\nLorna... We'll be seeing him every day.\n\n" +
		"2\n00:08:25,320 --> 00:08:29,760\nJust calm down, yeah?\nBut if he's good-looking and single,\nyes, please!\n\n" +
		"3\n00:08:29,760 --> 00:08:31,000\nNext cue.\n\n"
	if string(srt) != expected {
		t.Errorf("SRT output:\n%q\nwant:\n%q", srt, expected)
	}
}

// TestTTMLToSRT_SameTimingAcrossDivsDoesNotMerge pins the merge to a
// run of adjacent paragraphs: a timing match is only merged when the
// paragraphs are consecutive. A non-adjacent repeat of an earlier
// timing (here separated by a different cue) stays a separate cue.
func TestTTMLToSRT_SameTimingAcrossDivsDoesNotMerge(t *testing.T) {
	ttml := `<?xml version="1.0"?>
<tt xmlns="http://www.w3.org/ns/ttml">
  <body>
    <div>
      <p begin="00:00:01.000" end="00:00:02.000">A</p>
      <p begin="00:00:02.000" end="00:00:03.000">B</p>
      <p begin="00:00:01.000" end="00:00:02.000">C</p>
    </div>
  </body>
</tt>`
	srt, err := TTMLToSRT([]byte(ttml))
	if err != nil {
		t.Fatalf("TTMLToSRT: %v", err)
	}
	expected := "1\n00:00:01,000 --> 00:00:02,000\nA\n\n2\n00:00:02,000 --> 00:00:03,000\nB\n\n3\n00:00:01,000 --> 00:00:02,000\nC\n\n"
	if string(srt) != expected {
		t.Errorf("SRT output:\n%q\nwant:\n%q", srt, expected)
	}
}
