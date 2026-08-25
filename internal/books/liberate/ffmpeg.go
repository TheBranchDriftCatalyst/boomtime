// ffmpeg.go — the ffmpeg-backed Decryptor.
//
// The remux is `-c:a copy`: no re-encode, so it is I/O-bound and the audio is
// bit-identical to what Audible served. ffmpeg does the AES-CBC work given
// -audible_key/-audible_iv.
//
// GOTCHA — those options live on the **mov** demuxer, NOT on the demuxer called
// "aax". An AAXC file is an MP4/ISO-BMFF container, so mov handles it. ffmpeg
// DOES ship a demuxer literally named `aax`, but that is CRI AAX, an unrelated
// Criware game-audio format, and `ffmpeg -h demuxer=aax` will happily describe
// it while telling you nothing about Audible. Verify with:
//
//	ffmpeg -h demuxer=mov | grep audible
//
// Verified present in alpine 3.20's ffmpeg 6.1.1 (the runtime image) and in
// ffmpeg 9.0 locally. The same demuxer also exposes -activation_bytes, which is
// what legacy pre-AAXC titles need — so epic C's AAX support needs no extra
// dependency, only the code path.
//
// SECRET HANDLING — the one genuine wart in this approach. ffmpeg takes the
// content key as a COMMAND-LINE ARGUMENT, which means it is visible in the
// process table (/proc/<pid>/cmdline) for the lifetime of the remux. ffmpeg's
// aax demuxer offers no file- or env-based alternative, so this is inherent to
// the ffmpeg decision rather than something to code around; the native remuxer
// (epic D) would eliminate it. In a single-process container the exposure is
// small, but two rules follow and are enforced here:
//
//  1. the argv is NEVER logged — logArgs() redacts before anything is emitted;
//  2. ffmpeg's stderr is scrubbed before it lands in an error, because a usage
//     or parse error can echo the offending argument back at us.
package liberate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// FFmpegDecryptor implements Decryptor by shelling out.
type FFmpegDecryptor struct {
	// Path to the binary; empty means "ffmpeg" on PATH.
	Path string
}

var _ Decryptor = (*FFmpegDecryptor)(nil)

// NewFFmpegDecryptor builds the decryptor. Path defaults to "ffmpeg".
func NewFFmpegDecryptor(path string) *FFmpegDecryptor {
	if strings.TrimSpace(path) == "" {
		path = "ffmpeg"
	}
	return &FFmpegDecryptor{Path: path}
}

// Available runs `ffmpeg -version`. The server calls this at startup when
// liberation is enabled so a missing binary is a clear boot-time ERROR rather
// than a mystery failure on the first book — the same posture as the
// BOOM_ENCRYPTION_KEY prod gate.
func (d *FFmpegDecryptor) Available(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, d.Path, "-version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("liberate: ffmpeg not usable at %q: %w", d.Path, err)
	}
	if !bytes.Contains(bytes.ToLower(out), []byte("ffmpeg version")) {
		return fmt.Errorf("liberate: %q does not look like ffmpeg", d.Path)
	}
	return nil
}

// Decrypt strips the DRM and writes a tagged, chaptered M4B.
func (d *FFmpegDecryptor) Decrypt(ctx context.Context, req DecryptRequest) error {
	if req.SrcPath == "" || req.DstPath == "" {
		return errors.New("liberate: ffmpeg: src and dst are required")
	}
	if !req.Key.Valid() {
		// Deliberately does not print the key or its contents.
		return errors.New("liberate: ffmpeg: content key is missing or the wrong size")
	}
	args := d.buildArgs(req)

	cmd := exec.CommandContext(ctx, d.Path, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Scrub before this text can reach a log or an API response.
		detail := scrubSecrets(stderr.String(), req.Key)
		return fmt.Errorf("liberate: ffmpeg remux failed: %w: %s", err, truncate(strings.TrimSpace(detail), 2000))
	}
	return nil
}

// buildArgs assembles the argv. Input ORDER determines the -map indices, so the
// inputs and the maps are built together rather than in separate passes.
func (d *FFmpegDecryptor) buildArgs(req DecryptRequest) []string {
	args := []string{
		"-nostdin", "-y", "-hide_banner", "-loglevel", "error",
		// The AES material. Must precede -i: they configure the demuxer for the
		// input that follows.
		"-audible_key", req.Key.HexKey(),
		"-audible_iv", req.Key.HexIV(),
		"-i", req.SrcPath,
	}
	next := 1
	chapterIdx, coverIdx := -1, -1
	if req.FFMetadataPath != "" {
		args = append(args, "-i", req.FFMetadataPath)
		chapterIdx = next
		next++
	}
	if req.CoverPath != "" {
		args = append(args, "-i", req.CoverPath)
		coverIdx = next
		next++
	}

	// Take the audio from input 0 only. Without an explicit map, ffmpeg's
	// stream selection could pick up the cover as a video stream in a way that
	// confuses audiobook players.
	args = append(args, "-map", "0:a")
	if coverIdx >= 0 {
		args = append(args,
			"-map", strconv.Itoa(coverIdx)+":v",
			"-disposition:v", "attached_pic",
			"-c:v", "copy",
		)
	}
	if chapterIdx >= 0 {
		args = append(args, "-map_metadata", strconv.Itoa(chapterIdx))
	} else {
		// Strip the source metadata rather than passing it through: AAXC carries
		// Audible's own tagging, and letting it survive means a book is
		// inconsistently tagged depending on whether chapters happened to exist.
		args = append(args, "-map_metadata", "-1")
	}

	args = append(args, "-c:a", "copy")
	args = append(args, req.Meta.FFmpegTagArgs()...)
	// faststart moves the moov atom to the front so a player can start without
	// reading the whole file — it matters for a 600 MB book streamed over NFS.
	args = append(args, "-movflags", "+faststart", req.DstPath)
	return args
}

// logArgs returns the argv with the key material replaced, for the rare case a
// caller wants to record what was run. Use this and never the raw args.
func logArgs(args []string, key ContentKey) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i, a := range out {
		if a == key.HexKey() && key.HexKey() != "" {
			out[i] = "[redacted-key]"
		}
		if a == key.HexIV() && key.HexIV() != "" {
			out[i] = "[redacted-iv]"
		}
	}
	return out
}

// scrubSecrets removes key material from arbitrary text (ffmpeg stderr), so a
// usage error that echoes an argument back cannot leak it into a log.
func scrubSecrets(s string, key ContentKey) string {
	if k := key.HexKey(); k != "" {
		s = strings.ReplaceAll(s, k, "[redacted-key]")
	}
	if iv := key.HexIV(); iv != "" {
		s = strings.ReplaceAll(s, iv, "[redacted-iv]")
	}
	return s
}
