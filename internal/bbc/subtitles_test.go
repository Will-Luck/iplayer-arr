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

// TestTTMLToSRT_BrIsALineBreak: the BBC authors line breaks inside a
// paragraph with <br/> between styled spans. Those breaks carry
// meaning (speaker changes, dialogue splits) and must survive as SRT
// line breaks rather than being flattened into one space-joined line.
// Adjacent spans with no <br/> between them stay on one line, with
// the whitespace exactly as authored (BBC puts the separating space
// inside the second span). Fixtures are verbatim from Waterloo Road
// S1E1 (p00c9rpm).
func TestTTMLToSRT_BrIsALineBreak(t *testing.T) {
	ttml := `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns="http://www.w3.org/ns/ttml" xmlns:tts="http://www.w3.org/ns/ttml#styling">
  <body>
    <div>
      <p xml:id="C2_1" region="agr_0_20_39_4_after" style="S2 agtas_right" begin="00:00:57.280" end="00:00:59.680"><span style="S4">Look, sir.</span><br/><span style="S4">The headmaster's on the roof.</span></p>
      <p xml:id="C3_1" region="R9" style="S2" begin="00:01:00.000" end="00:01:02.000"><span style="S5">And your full support?</span><span style="S3"> Absolutely.</span></p>
      <p xml:id="C4_1" region="R9" style="S2" begin="00:01:03.000" end="00:01:05.000"><span style="S5">Who keeps soiling all this paper?</span><br/><span style="S5">I've not got a dirty bum,</span><br/><span style="S5">I'm the head!</span></p>
      <p xml:id="C5_1" region="R9" style="S2" begin="00:01:06.000" end="00:01:08.000"><span style="S7">No.</span><span style="S3"> You think I'm turning</span><br/><span style="S3">into a &quot;wife&quot;.</span><span style="S7"> I'm not blaming you.</span></p>
    </div>
  </body>
</tt>`
	srt, err := TTMLToSRT([]byte(ttml))
	if err != nil {
		t.Fatalf("TTMLToSRT: %v", err)
	}
	expected := "1\n00:00:57,280 --> 00:00:59,680\nLook, sir.\nThe headmaster's on the roof.\n\n" +
		"2\n00:01:00,000 --> 00:01:02,000\nAnd your full support? Absolutely.\n\n" +
		"3\n00:01:03,000 --> 00:01:05,000\nWho keeps soiling all this paper?\nI've not got a dirty bum,\nI'm the head!\n\n" +
		"4\n00:01:06,000 --> 00:01:08,000\nNo. You think I'm turning\ninto a \"wife\". I'm not blaming you.\n\n"
	if string(srt) != expected {
		t.Errorf("SRT output:\n%q\nwant:\n%q", srt, expected)
	}
}

// TestTTMLToSRT_EscapedAngleBracketsSurvive: text containing &lt; and
// &gt; must come out as literal < and >, not be eaten as if it were a
// tag. Also pins that source-formatting whitespace (newlines and
// indentation between spans) collapses to a single space rather than
// becoming a line break: only <br/> is a line break.
func TestTTMLToSRT_EscapedAngleBracketsSurvive(t *testing.T) {
	ttml := `<?xml version="1.0"?>
<tt xmlns="http://www.w3.org/ns/ttml">
  <body>
    <div>
      <p begin="00:00:01.000" end="00:00:02.000">
        <span>Press &lt;A&gt; to start</span>
        <span>then &lt;B&gt;.</span>
      </p>
      <p begin="00:00:03.000" end="00:00:04.000">Tom &amp; Jerry<br />
        <span>  spaced   out  </span></p>
    </div>
  </body>
</tt>`
	srt, err := TTMLToSRT([]byte(ttml))
	if err != nil {
		t.Fatalf("TTMLToSRT: %v", err)
	}
	expected := "1\n00:00:01,000 --> 00:00:02,000\nPress <A> to start then <B>.\n\n" +
		"2\n00:00:03,000 --> 00:00:04,000\nTom & Jerry\nspaced out\n\n"
	if string(srt) != expected {
		t.Errorf("SRT output:\n%q\nwant:\n%q", srt, expected)
	}
}
