package download

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// GitHub #59: BBC's HLS packager writes TS segments whose video PTS has
// been moved back by the first frame's composition offset while DTS has
// not, so most B-frames carry DTS > PTS. A plain -c copy remux then has
// ffmpeg rewrite each of those packets to pts = dts = median(pts, dts,
// last_dts+1), and the MP4 ends up variable frame rate (1-tick and 2-3
// frame sample durations, frames shown out of step), which the reporter
// saw stutter in Plex and VLC.
func TestBuildFFmpegArgs_IgnoresContainerDTSBeforeInput(t *testing.T) {
	args := buildFFmpegArgs("https://cdn.example/stream.m3u8", "/out/file.mp4")
	f, i := argIndex(args, "-fflags"), argIndex(args, "-i")
	if f < 0 || f+1 >= len(args) || !slices.Contains(strings.Split(strings.TrimPrefix(args[f+1], "+"), "+"), "igndts") {
		t.Fatalf("buildFFmpegArgs must pass -fflags +igndts: %v", args)
	}
	if f > i {
		t.Errorf("-fflags (%d) is an input option and must precede -i (%d): %v", f, i, args)
	}
}

// gh59Shape is one encode shape for the exec tests. The reorder depth
// (how many frames a decoder holds back) sets how far the BBC packager
// moves PTS back and so which bad sample durations ffmpeg's guess
// produces; real BBC encodes use depths 1 to 3. libx264 signals at most
// 2 (b-pyramid) whatever bframes is (checked with 3, 7 and 16), so depth
// 3, which The Traitors S02E01 uses, was only checked on real BBC data.
type gh59Shape struct {
	name string
	fps  int
	x264 string
	// ptsOnly drops the DTS field from every video PES whose rewritten
	// PTS equals its DTS, as BBC's packager does (no DTS on the IDR and
	// on some B-frames: 200 of 1536 PES on The Traitors). That sends the
	// demuxer down its PTS-only path, where DTS is taken to equal PTS.
	// Here it hits the IDRs and the last P-frame before each IDR, plus, in
	// the 3-B-frame pyramid, the reference B-frame of every mini-GOP, so
	// B-frames take the PTS-only path as they do on BBC.
	ptsOnly bool
}

var gh59Shapes = []gh59Shape{
	// Depth 1 at 25 fps: two plain B-frames, no pyramid, the depth BBC
	// uses for films, Bluey and archive EastEnders.
	{name: "reorder1_25fps", fps: 25, ptsOnly: true,
		x264: "keyint=25:min-keyint=25:scenecut=0:bframes=2:b-adapt=0:b-pyramid=none"},
	// Depth 2 at 50 fps with a B-pyramid: news, soaps, sport, HIGNFY.
	// One case drops the DTS where BBC does, one keeps it on every PES.
	{name: "reorder2_50fps", fps: 50, ptsOnly: true,
		x264: "keyint=50:min-keyint=50:scenecut=0:bframes=3:b-adapt=0:b-pyramid=normal"},
	{name: "reorder2_50fps_dts_everywhere", fps: 50,
		x264: "keyint=50:min-keyint=50:scenecut=0:bframes=3:b-adapt=0:b-pyramid=normal"},
	// Seven B-frames per P, like The Traitors' 8-frame mini-GOP. x264
	// still signals depth 2, but the longer mini-GOP puts frames in
	// other places relative to the guess, so it bends a different set of
	// sample durations.
	{name: "seven_bframes_50fps", fps: 50, ptsOnly: true,
		x264: "keyint=50:min-keyint=50:scenecut=0:bframes=7:b-adapt=0:b-pyramid=normal"},
}

// Each shape is a 2 s libx264 HLS stream in two segments with the BBC
// defect written into its PES headers. Without the fix it must reproduce
// the #59 symptom, so the test cannot pass on a fixture that lacks the
// defect. With buildFFmpegArgs the MP4 must have one sample duration
// (one frame), keep every frame's presentation time, keep the A/V offset,
// and RunFFmpeg must still see progress lines.
func TestRunFFmpeg_BBCPTSBeforeDTSGivesConstantFrameRate(t *testing.T) {
	gh59NeedFFmpeg(t)
	for _, sh := range gh59Shapes {
		t.Run(sh.name, func(t *testing.T) {
			// Parallel within this test only: nothing else in the package
			// is parallel, so the package vars RunFFmpeg reads (watchdog
			// interval, finalize grace) cannot be mid-swap by another test.
			t.Parallel()
			dir := t.TempDir()
			segs := gh59Encode(t, dir, sh, true)
			src := gh59WriteDefect(t, segs, sh.ptsOnly)
			srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
			defer srv.Close()
			url := srv.URL + "/index.m3u8"

			// Capable-fixture guard: the same remux without the fix must
			// still come out variable frame rate. If a future ffmpeg stops
			// guessing, this fails loudly rather than letting the test pass
			// on a fixture that no longer proves anything.
			before := filepath.Join(dir, "before.mp4")
			gh59Remux(t, gh59WithoutFFlags(buildFFmpegArgs(url, before)))
			unfixed := gh59Deltas(gh59Probe(t, before, "v:0").dts)
			if len(unfixed) < 2 {
				t.Fatalf("fixture no longer reproduces #59 without the fix (DTS deltas %v)", unfixed)
			}
			t.Logf("without the fix: video DTS deltas %v", unfixed)

			after := filepath.Join(dir, "after.mp4")
			progress := 0
			var last FFmpegProgress
			err := RunFFmpeg(context.Background(), FFmpegJob{
				StreamURL:  url,
				OutputPath: after,
				OnProgress: func(p FFmpegProgress) { progress++; last = p },
				Duration:   2 * time.Second,
			})
			if err != nil {
				t.Fatalf("RunFFmpeg: %v", err)
			}
			if progress == 0 {
				t.Error("RunFFmpeg parsed no progress lines")
			}
			// The final muxed time= feeds the GitHub #50 faststart grace
			// (crossedFaststartThreshold), so the fix must not pull it short
			// of the programme's end. Within two frames of the 2 s end, not
			// the 99% threshold itself: on a clip this short that is 20 ms,
			// and ffmpeg 8.0.1 reports 1.98 s for the 50 fps shapes (one
			// frame short; 6.1 reports 2.00 s), right on the line. On a real
			// programme the threshold leaves tens of seconds of slack.
			t.Logf("final muxed time=%.3fs", last.TimeSeconds)
			if tol := 2.0 / float64(sh.fps); last.TimeSeconds < 2-tol || last.TimeSeconds > 2+tol {
				t.Errorf("final muxed time=%.3fs, want 2s within %.3fs", last.TimeSeconds, tol)
			}

			v := gh59Probe(t, after, "v:0")
			frame := int64(90000 / sh.fps)
			if d := gh59Deltas(v.dts); len(d) != 1 || v.at90k(d[0]) != frame {
				t.Errorf("video sample durations %v (time base %d/%d), want only one frame, %d ticks at 90 kHz", d, v.tbNum, v.tbDen, frame)
			}
			gotPTS := slices.Clone(v.pts)
			slices.Sort(gotPTS)
			if d := gh59Deltas(gotPTS); len(d) != 1 {
				t.Errorf("presentation times are not evenly spaced: %v", d)
			}
			wantPTS := slices.Clone(src.videoPTS)
			slices.Sort(wantPTS)
			if len(gotPTS) != len(wantPTS) {
				t.Fatalf("video frames: got %d, want %d", len(gotPTS), len(wantPTS))
			}
			for i := range gotPTS {
				// Every frame keeps its own presentation time (to the 90 kHz tick).
				if got, want := v.at90k(gotPTS[i]-gotPTS[0]), wantPTS[i]-wantPTS[0]; got != want {
					t.Fatalf("frame %d presented at %d ticks, want %d", i, got, want)
				}
			}
			a := gh59Probe(t, after, "a:0")
			gotAV := v.seconds(gotPTS[0]) - a.seconds(a.pts[0])
			wantAV := float64(wantPTS[0]-src.firstAudioPTS) / 90000
			if diff := gotAV - wantAV; diff > 0.001 || diff < -0.001 {
				t.Errorf("video starts %.3fs after audio, want %.3fs", gotAV, wantAV)
			}
		})
	}
}

// The fix must be harmless if the BBC repairs its packager. Each shape is
// encoded with libx264's own timestamps (DTS exactly the reorder depth
// behind PTS, as a correct packager writes them) and left alone; the
// buildFFmpegArgs remux must then give the same video and audio packet
// timestamps with and without -fflags +igndts.
//
// The stream must be one the default libx264 timing produces: holding DTS
// back further than the reorder depth (the setts trick the defect fixture
// needs) is valid input too, but igndts then rebuilds the shorter delay,
// so the MP4's DTS and edit list move by a frame while PTS stays put.
func TestBuildFFmpegArgs_IgnDTSLeavesValidTimestampsAlone(t *testing.T) {
	gh59NeedFFmpeg(t)
	seen := map[string]bool{}
	for _, sh := range gh59Shapes {
		// ptsOnly only matters once the defect is written, so shapes that
		// differ in nothing else are the same control.
		key := fmt.Sprint(sh.fps, sh.x264)
		if seen[key] {
			continue
		}
		seen[key] = true
		t.Run(sh.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			gh59Encode(t, dir, sh, false)
			srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
			defer srv.Close()
			url := srv.URL + "/index.m3u8"

			withArgs := buildFFmpegArgs(url, filepath.Join(dir, "with.mp4"))
			withoutArgs := gh59WithoutFFlags(buildFFmpegArgs(url, filepath.Join(dir, "without.mp4")))
			if len(withoutArgs) == len(withArgs) {
				// Nothing was stripped, so the two runs below would be the
				// same command and could not disagree.
				t.Fatalf("buildFFmpegArgs passes no -fflags, so this comparison proves nothing: %v", withArgs)
			}
			gh59Remux(t, withArgs)
			gh59Remux(t, withoutArgs)

			for _, sel := range []string{"v:0", "a:0"} {
				w := gh59Probe(t, filepath.Join(dir, "with.mp4"), sel)
				wo := gh59Probe(t, filepath.Join(dir, "without.mp4"), sel)
				if !slices.Equal(w.pts, wo.pts) || !slices.Equal(w.dts, wo.dts) || w.tbNum != wo.tbNum || w.tbDen != wo.tbDen {
					t.Errorf("%s packet timestamps differ with -fflags +igndts:\n with    pts %v\n         dts %v\n without pts %v\n         dts %v",
						sel, w.pts, w.dts, wo.pts, wo.dts)
				}
				// Capable-control guard: a stream with no reordered frames
				// has PTS == DTS everywhere, and dropping DTS could not change
				// it even if the rebuild were wrong.
				if sel == "v:0" && slices.Equal(w.pts, w.dts) {
					t.Fatalf("control video has no reordered frames, so it cannot show a DTS change")
				}
			}

			// Valid input whose DTS lags more than the reorder depth (what
			// gh59Encode's holdDTS writes, and what a repaired packager could
			// still write). igndts rebuilds the shorter delay, so video DTS
			// may move by a frame, but every frame's presentation time and
			// the whole audio track must stay exactly as they were.
			heldDir := t.TempDir()
			gh59Encode(t, heldDir, sh, true)
			heldSrv := httptest.NewServer(http.FileServer(http.Dir(heldDir)))
			defer heldSrv.Close()
			heldURL := heldSrv.URL + "/index.m3u8"
			gh59Remux(t, buildFFmpegArgs(heldURL, filepath.Join(heldDir, "with.mp4")))
			gh59Remux(t, gh59WithoutFFlags(buildFFmpegArgs(heldURL, filepath.Join(heldDir, "without.mp4"))))
			hv := gh59Probe(t, filepath.Join(heldDir, "with.mp4"), "v:0")
			hvo := gh59Probe(t, filepath.Join(heldDir, "without.mp4"), "v:0")
			if !slices.Equal(hv.pts, hvo.pts) || hv.tbNum != hvo.tbNum || hv.tbDen != hvo.tbDen {
				t.Errorf("held-DTS input: video PTS differ with -fflags +igndts:\n with    %v\n without %v", hv.pts, hvo.pts)
			}
			ha := gh59Probe(t, filepath.Join(heldDir, "with.mp4"), "a:0")
			hao := gh59Probe(t, filepath.Join(heldDir, "without.mp4"), "a:0")
			if !slices.Equal(ha.pts, hao.pts) || !slices.Equal(ha.dts, hao.dts) || ha.tbNum != hao.tbNum || ha.tbDen != hao.tbDen {
				t.Errorf("held-DTS input: audio timestamps differ with -fflags +igndts")
			}
		})
	}
}

func gh59NeedFFmpeg(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH", bin)
		}
	}
}

// gh59WithoutFFlags returns args with the -fflags option and its value
// removed: the v1.8.2 command line, for the before and control runs.
func gh59WithoutFFlags(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "-fflags" {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func gh59Remux(t *testing.T, args []string) {
	t.Helper()
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
}

// gh59Encode writes a 2 s, two-segment HLS stream of the shape into dir
// (index.m3u8 plus its segments, whose paths it returns). Video is lavfi
// testsrc2 and audio a sine, so nothing copyrighted is involved and no
// binary fixture is committed.
//
// With holdDTS, setts holds every DTS one frame further back than libx264
// needs. ffmpeg's mpegts muxer writes a DTS field only when DTS != PTS, so
// without the hold the frames libx264 decodes at their presentation time
// would have no DTS field for gh59WriteDefect to keep once their PTS has
// moved below it. The extra frame also makes the shift one frame larger.
func gh59Encode(t *testing.T, dir string, sh gh59Shape, holdDTS bool) []string {
	t.Helper()
	args := []string{"-v", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=160x90:rate=%d:duration=2", sh.fps),
		"-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=48000:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-x264-params", sh.x264,
	}
	if holdDTS {
		// PTS moves too, by one frame more than DTS, so the hold never
		// pushes the first DTS below zero.
		args = append(args, "-bsf:v", fmt.Sprintf("setts=pts=PTS+3/(%d*TB):dts=DTS+2/(%d*TB)", sh.fps, sh.fps))
	}
	args = append(args,
		"-c:a", "aac", "-b:a", "64k",
		"-f", "hls", "-hls_time", "1", "-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(dir, "seg%d.ts"),
		filepath.Join(dir, "index.m3u8"))
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("generate fixture: %v\n%s", err, out)
	}
	playlist, err := os.ReadFile(filepath.Join(dir, "index.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	var segs []string
	for _, line := range strings.Split(string(playlist), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			segs = append(segs, filepath.Join(dir, line))
		}
	}
	if len(segs) < 2 {
		t.Fatalf("want at least 2 segments so the remux crosses a join, got %v", segs)
	}
	return segs
}

type gh59Source struct {
	videoPTS      []int64 // 90 kHz, after the defect is written
	firstAudioPTS int64
}

// gh59WriteDefect rewrites the segments in place the way BBC's packager
// does: each video PTS moves back by the first frame's PTS-DTS and DTS
// stays, so the IDR ends with PTS == DTS and the B-frames below it with
// PTS < DTS. Audio is left alone. Every video PES must carry a DTS (see
// gh59Encode's holdDTS); with ptsOnly, any PES whose new PTS equals its
// DTS then loses the DTS field again, as BBC writes them.
func gh59WriteDefect(t *testing.T, segs []string, ptsOnly bool) gh59Source {
	t.Helper()
	var src gh59Source
	shift, haveAudio, lagging, dropped := int64(-1), false, 0, 0
	for _, seg := range segs {
		b, err := os.ReadFile(seg)
		if err != nil {
			t.Fatal(err)
		}
		for off := 0; off+188 <= len(b); off += 188 {
			p := b[off : off+188]
			if p[0] != 0x47 {
				t.Fatalf("%s: lost TS sync at %d", seg, off)
			}
			if p[1]&0x40 == 0 || p[3]&0x10 == 0 { // no PES start or no payload
				continue
			}
			i := 4
			if p[3]&0x20 != 0 {
				i += 1 + int(p[4])
			}
			if i+19 > len(p) || p[i] != 0 || p[i+1] != 0 || p[i+2] != 1 || p[i+7]&0x80 == 0 {
				continue
			}
			q, sid := p[i:], p[i+3]
			pts := gh59ReadTS(q[9:14])
			switch {
			case sid&0xF0 == 0xE0:
				// The in-place rewrite below relies on the header holding
				// exactly PTS then DTS (flags 0xC0, 10 header bytes), which
				// is what ffmpeg's mpegts muxer writes for H.264.
				if q[7] != 0xC0 || q[8] != 10 {
					t.Fatalf("%s: video PES header flags %#x, length %d; the fixture needs PTS and DTS on every frame", seg, q[7], q[8])
				}
				dts := gh59ReadTS(q[14:19])
				if shift < 0 {
					shift = pts - dts
				}
				pts -= shift
				if pts < dts {
					lagging++
				}
				gh59WriteTS(q[9:14], pts)
				if ptsOnly && pts == dts {
					// PTS only: flags '10' and the PTS field's own '0010'
					// prefix. The DTS bytes become header stuffing (0xFF),
					// which the header length already covers and demuxers
					// skip, so the packet keeps its size.
					q[7] = 0x80
					q[9] = q[9]&0x0F | 0x20
					copy(q[14:19], []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
					dropped++
				}
				src.videoPTS = append(src.videoPTS, pts)
			case sid&0xE0 == 0xC0 && !haveAudio:
				src.firstAudioPTS, haveAudio = pts, true
			}
		}
		if err := os.WriteFile(seg, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if shift <= 0 || lagging == 0 || !haveAudio {
		t.Fatalf("fixture lacks the defect: shift %d, %d frames with PTS < DTS, audio %v", shift, lagging, haveAudio)
	}
	// The IDRs at least end up with PTS == DTS, so a PTS-only case that
	// dropped nothing would silently test the DTS-everywhere path again.
	if ptsOnly && dropped == 0 {
		t.Fatalf("fixture has no PTS-only video PES")
	}
	t.Logf("fixture: PTS moved back %d ticks; of %d video PES, %d have PTS < DTS and %d carry PTS only",
		shift, len(src.videoPTS), lagging, dropped)
	return src
}

func gh59ReadTS(b []byte) int64 {
	return int64(b[0]>>1&7)<<30 | int64(b[1])<<22 | int64(b[2]>>1)<<15 | int64(b[3])<<7 | int64(b[4]>>1)
}

func gh59WriteTS(b []byte, v int64) {
	b[0] = b[0]&0xF0 | byte(v>>29&0x0E) | 1
	b[1] = byte(v >> 22)
	b[2] = byte(v>>14&0xFE) | 1
	b[3] = byte(v >> 7)
	b[4] = byte(v<<1&0xFE) | 1
}

type gh59Stream struct {
	pts, dts []int64
	tbNum    int64
	tbDen    int64
}

func (s gh59Stream) seconds(ts int64) float64 { return float64(ts*s.tbNum) / float64(s.tbDen) }
func (s gh59Stream) at90k(ts int64) int64     { return ts * s.tbNum * 90000 / s.tbDen }

func gh59Probe(t *testing.T, path, sel string) gh59Stream {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", sel,
		"-show_entries", "stream=time_base", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var s gh59Stream
	num, den, ok := strings.Cut(strings.TrimSpace(string(out)), "/")
	s.tbNum, _ = strconv.ParseInt(num, 10, 64)
	s.tbDen, _ = strconv.ParseInt(den, 10, 64)
	if !ok || s.tbNum <= 0 || s.tbDen <= 0 {
		t.Fatalf("ffprobe %s: time base %q", path, out)
	}
	out, err = exec.Command("ffprobe", "-v", "error", "-select_streams", sel,
		"-show_entries", "packet=pts,dts", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	for _, line := range strings.Fields(string(out)) {
		f := strings.Split(line, ",")
		if len(f) < 2 {
			t.Fatalf("ffprobe %s: packet line %q", path, line)
		}
		pts, err1 := strconv.ParseInt(f[0], 10, 64)
		dts, err2 := strconv.ParseInt(f[1], 10, 64)
		if err1 != nil || err2 != nil {
			t.Fatalf("ffprobe %s: packet line %q", path, line)
		}
		s.pts, s.dts = append(s.pts, pts), append(s.dts, dts)
	}
	if len(s.pts) == 0 {
		t.Fatalf("ffprobe %s: no %s packets", path, sel)
	}
	return s
}

// gh59Deltas returns the distinct differences between neighbours, in order.
func gh59Deltas(ts []int64) []int64 {
	var d []int64
	for i := 1; i < len(ts); i++ {
		if x := ts[i] - ts[i-1]; !slices.Contains(d, x) {
			d = append(d, x)
		}
	}
	return d
}
