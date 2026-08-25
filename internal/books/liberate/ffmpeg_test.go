package liberate

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testKey(t *testing.T) ContentKey {
	t.Helper()
	k, _ := hex.DecodeString(wantKeyHex)
	iv, _ := hex.DecodeString(wantIVHex)
	return ContentKey{Key: k, IV: iv}
}

// indexOf returns the position of the first occurrence of v, or -1.
func indexOf(args []string, v string) int {
	for i, a := range args {
		if a == v {
			return i
		}
	}
	return -1
}

func TestFFmpegBuildArgs(t *testing.T) {
	d := NewFFmpegDecryptor("")
	key := testKey(t)

	t.Run("audio only", func(t *testing.T) {
		args := d.buildArgs(DecryptRequest{
			SrcPath: "/w/in.aaxc", DstPath: "/w/out.m4b", Key: key,
			Meta: Metadata{Title: "T", Authors: []string{"A"}},
		})
		// -c:a copy is the whole reason this is fast and lossless. If it ever
		// disappears, every liberation silently becomes a re-encode.
		if i := indexOf(args, "-c:a"); i < 0 || args[i+1] != "copy" {
			t.Error("missing -c:a copy — the remux would re-encode")
		}
		// The key material must precede -i: they configure the demuxer for the
		// input that follows it.
		if indexOf(args, "-audible_key") > indexOf(args, "-i") {
			t.Error("-audible_key must come before -i")
		}
		// With no chapter input, source metadata is stripped rather than passed
		// through, so tagging is consistent regardless of chapter availability.
		if i := indexOf(args, "-map_metadata"); i < 0 || args[i+1] != "-1" {
			t.Error("expected -map_metadata -1 when no chapters are supplied")
		}
		if args[len(args)-1] != "/w/out.m4b" {
			t.Errorf("output must be the final argument, got %q", args[len(args)-1])
		}
		if i := indexOf(args, "-movflags"); i < 0 || args[i+1] != "+faststart" {
			t.Error("missing +faststart")
		}
	})

	t.Run("with chapters and cover", func(t *testing.T) {
		args := d.buildArgs(DecryptRequest{
			SrcPath: "/w/in.aaxc", DstPath: "/w/out.m4b", Key: key,
			FFMetadataPath: "/w/ch.txt", CoverPath: "/w/cover.jpg",
			Meta: Metadata{Title: "T"},
		})
		// Input order fixes the map indices: 0=audio, 1=chapters, 2=cover.
		if i := indexOf(args, "-map_metadata"); i < 0 || args[i+1] != "1" {
			t.Errorf("chapters should map from input 1, got %v", args)
		}
		if i := indexOf(args, "-disposition:v"); i < 0 || args[i+1] != "attached_pic" {
			t.Error("cover must be marked attached_pic or players show it as a video stream")
		}
		if indexOf(args, "2:v") < 0 {
			t.Error("cover should map from input 2")
		}
		if indexOf(args, "0:a") < 0 {
			t.Error("audio must be explicitly mapped from input 0")
		}
	})
}

// The key is on the command line and therefore in the process table — that is
// inherent to ffmpeg. What we control is that it never reaches a LOG.
func TestFFmpegRedaction(t *testing.T) {
	key := testKey(t)
	args := NewFFmpegDecryptor("").buildArgs(DecryptRequest{
		SrcPath: "/w/in.aaxc", DstPath: "/w/out.m4b", Key: key,
	})

	// Sanity: the raw argv genuinely does contain the material, so the redaction
	// below is doing real work rather than passing vacuously.
	if indexOf(args, wantKeyHex) < 0 {
		t.Fatal("test precondition failed: argv does not contain the key")
	}

	safe := strings.Join(logArgs(args, key), " ")
	if strings.Contains(safe, wantKeyHex) || strings.Contains(safe, wantIVHex) {
		t.Errorf("logArgs leaked key material: %s", safe)
	}
	if !strings.Contains(safe, "[redacted-key]") || !strings.Contains(safe, "[redacted-iv]") {
		t.Errorf("logArgs did not mark redaction: %s", safe)
	}

	// ffmpeg can echo a bad argument back in its error output.
	dirty := "Option not found: " + wantKeyHex + " and iv " + wantIVHex
	clean := scrubSecrets(dirty, key)
	if strings.Contains(clean, wantKeyHex) || strings.Contains(clean, wantIVHex) {
		t.Errorf("scrubSecrets leaked: %s", clean)
	}
}

func TestFFmpegAvailable(t *testing.T) {
	ctx := context.Background()
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		if err := NewFFmpegDecryptor("").Available(ctx); err != nil {
			t.Errorf("Available() on a real ffmpeg = %v", err)
		}
	} else {
		t.Log("no ffmpeg on PATH; skipping the positive case")
	}
	// The negative case needs no ffmpeg and is what the startup gate relies on.
	if err := NewFFmpegDecryptor("/nonexistent/ffmpeg-xyz").Available(ctx); err == nil {
		t.Error("Available() accepted a nonexistent binary")
	}
	// A binary that exists but is not ffmpeg must also be rejected.
	if err := NewFFmpegDecryptor("/bin/echo").Available(ctx); err == nil {
		t.Error("Available() accepted /bin/echo as ffmpeg")
	}
}

func TestDecryptRejectsBadInput(t *testing.T) {
	d := NewFFmpegDecryptor("")
	ctx := context.Background()

	if err := d.Decrypt(ctx, DecryptRequest{DstPath: "/w/out.m4b", Key: testKey(t)}); err == nil {
		t.Error("accepted an empty SrcPath")
	}
	err := d.Decrypt(ctx, DecryptRequest{SrcPath: "/w/in", DstPath: "/w/out", Key: ContentKey{}})
	if err == nil {
		t.Fatal("accepted an invalid content key")
	}
	// The error must not describe the key beyond "missing or wrong size".
	if strings.Contains(err.Error(), "0001") {
		t.Errorf("error leaked key bytes: %v", err)
	}
}

// INTEGRATION: prove BuildFFMetadata emits a document REAL ffmpeg accepts, and
// that the chapters survive into the output container. A unit test can only
// check that our writer agrees with our parser; this catches actual ffmetadata
// syntax errors, which is the failure mode that would otherwise only show up
// against a real audiobook.
func TestFFMetadataAcceptedByRealFFmpeg(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not on PATH")
	}
	ctx := context.Background()
	dir := t.TempDir()

	// 30s of silence stands in for the decrypted audio. The DRM strip is
	// ffmpeg's job and is not what this test is about; the chapter/tag muxing is.
	src := filepath.Join(dir, "silence.m4a")
	gen := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono", "-t", "30", "-c:a", "aac", src)
	if out, gerr := gen.CombinedOutput(); gerr != nil {
		t.Skipf("could not generate fixture audio: %v: %s", gerr, out)
	}

	ci := ChapterInfo{
		RuntimeLengthMs: 30000,
		Chapters: []Chapter{
			{Title: "Opening Credits", StartOffsetMs: 0, LengthMs: 5000},
			{Title: "Part One · Chapter 1", StartOffsetMs: 5000, LengthMs: 15000},
			{Title: `Tricky = title; with #hash`, StartOffsetMs: 20000, LengthMs: 10000},
		},
	}
	chapPath := filepath.Join(dir, "chapters.txt")
	if werr := os.WriteFile(chapPath, []byte(BuildFFMetadata(ci)), 0o644); werr != nil {
		t.Fatalf("write chapters: %v", werr)
	}

	meta := Metadata{
		Title: "The Gate of the Feral Gods", Authors: []string{"Matt Dinniman"},
		Narrators: []string{"Jeff Hays"}, Series: "Dungeon Crawler Carl",
		SeriesIndex: "04", Genre: "LitRPG", ReleaseDate: "2021-09-14",
	}
	dst := filepath.Join(dir, "out.m4b")

	// The real arg list minus the aax-demuxer options, which only apply to an
	// actually-encrypted input.
	args := []string{"-nostdin", "-y", "-hide_banner", "-loglevel", "error",
		"-i", src, "-i", chapPath, "-map", "0:a", "-map_metadata", "1", "-c:a", "copy"}
	args = append(args, meta.FFmpegTagArgs()...)
	args = append(args, "-movflags", "+faststart", dst)

	if out, merr := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); merr != nil {
		t.Fatalf("ffmpeg REJECTED our ffmetadata/tags: %v\n%s", merr, out)
	}

	probe := exec.CommandContext(ctx, ffprobe, "-v", "quiet", "-print_format", "json",
		"-show_chapters", "-show_format", dst)
	raw, perr := probe.Output()
	if perr != nil {
		t.Fatalf("ffprobe: %v", perr)
	}
	var got struct {
		Chapters []struct {
			StartTime string            `json:"start_time"`
			Tags      map[string]string `json:"tags"`
		} `json:"chapters"`
		Format struct {
			Tags map[string]string `json:"tags"`
		} `json:"format"`
	}
	if jerr := json.Unmarshal(raw, &got); jerr != nil {
		t.Fatalf("parse ffprobe json: %v", jerr)
	}

	if len(got.Chapters) != 3 {
		t.Fatalf("output has %d chapters, want 3", len(got.Chapters))
	}
	if title := got.Chapters[1].Tags["title"]; title != "Part One · Chapter 1" {
		t.Errorf("chapter 1 title = %q, want the part-prefixed name", title)
	}
	// The escaped title must round-trip to its ORIGINAL text, not the escaped form.
	if title := got.Chapters[2].Tags["title"]; title != `Tricky = title; with #hash` {
		t.Errorf("escaped title round-tripped as %q, want the original", title)
	}
	if st := got.Chapters[1].StartTime; !strings.HasPrefix(st, "5.0") {
		t.Errorf("chapter 1 start = %q, want 5.0s", st)
	}
	if got.Format.Tags["title"] != "The Gate of the Feral Gods" {
		t.Errorf("title tag = %q", got.Format.Tags["title"])
	}
	if got.Format.Tags["artist"] != "Matt Dinniman" {
		t.Errorf("artist tag = %q", got.Format.Tags["artist"])
	}
	if got.Format.Tags["album"] != "Dungeon Crawler Carl" {
		t.Errorf("album tag = %q, want the series", got.Format.Tags["album"])
	}
}
