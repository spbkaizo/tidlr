package tidal

import (
	"encoding/base64"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestParseBTSFlacManifest(t *testing.T) {
	manifest := `{"mimeType":"audio/flac","codecs":"flac","encryptionType":"NONE","urls":["https://x/0.flac?token=abc"]}`
	ts := TrackStream{
		TrackID:          540168118,
		AudioQuality:     "LOSSLESS",
		ManifestMimeType: "application/vnd.tidal.bts",
		Manifest:         b64(manifest),
	}
	info, err := ParseTrackStream(ts)
	if err != nil {
		t.Fatalf("ParseTrackStream: %v", err)
	}
	if info.Extension != ".flac" {
		t.Errorf("extension = %q, want .flac", info.Extension)
	}
	if len(info.URLs) != 1 || info.URLs[0] != "https://x/0.flac?token=abc" {
		t.Errorf("urls = %v", info.URLs)
	}
}

func TestParseBTSAtmosManifest(t *testing.T) {
	// Dolby Atmos comes as eac3 in mp4 -> .m4a.
	manifest := `{"mimeType":"audio/mp4","codecs":"eac3","encryptionType":"NONE","urls":["https://x/a.mp4"]}`
	ts := TrackStream{
		AudioQuality:     "LOW",
		ManifestMimeType: "application/vnd.tidal.bts",
		Manifest:         b64(manifest),
	}
	info, err := ParseTrackStream(ts)
	if err != nil {
		t.Fatalf("ParseTrackStream: %v", err)
	}
	if info.Extension != ".m4a" {
		t.Errorf("extension = %q, want .m4a", info.Extension)
	}
}

func TestParseDashManifest(t *testing.T) {
	// Minimal DASH manifest: 1 base segment + r=2 repeats => 3 timeline segments
	// => URLs for index 0..3 (tiddl's inclusive range).
	dash := `<?xml version="1.0"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011">
  <Period>
    <AdaptationSet>
      <Representation codecs="flac">
        <SegmentTemplate media="https://x/seg_$Number$.mp4">
          <SegmentTimeline>
            <S d="100" r="2"/>
          </SegmentTimeline>
        </SegmentTemplate>
      </Representation>
    </AdaptationSet>
  </Period>
</MPD>`
	ts := TrackStream{
		AudioQuality:     "HI_RES_LOSSLESS",
		ManifestMimeType: "application/dash+xml",
		Manifest:         b64(dash),
	}
	info, err := ParseTrackStream(ts)
	if err != nil {
		t.Fatalf("ParseTrackStream: %v", err)
	}
	// 1 + r(2) = 3 timeline segments; tiddl builds range(0, total+1) => 4 urls.
	if len(info.URLs) != 4 {
		t.Fatalf("expected 4 segment urls, got %d: %v", len(info.URLs), info.URLs)
	}
	if info.URLs[0] != "https://x/seg_0.mp4" || info.URLs[3] != "https://x/seg_3.mp4" {
		t.Errorf("url expansion wrong: %v", info.URLs)
	}
	// HI_RES_LOSSLESS FLAC-in-mp4 uses .m4a (remuxed to .flac after download).
	if info.Extension != ".m4a" {
		t.Errorf("extension = %q, want .m4a for HiRes FLAC-in-mp4", info.Extension)
	}
}
