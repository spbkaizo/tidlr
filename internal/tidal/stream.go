package tidal

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// dolbyCodecs are lossy Dolby Atmos codecs (delivered in an mp4 container).
var dolbyCodecs = map[string]bool{"eac3": true, "ac4": true}

// btsManifest is the JSON manifest for manifestMimeType application/vnd.tidal.bts.
type btsManifest struct {
	MimeType       string   `json:"mimeType"`
	Codecs         string   `json:"codecs"`
	EncryptionType string   `json:"encryptionType"`
	URLs           []string `json:"urls"`
}

// StreamInfo is the parsed result of a track manifest: the ordered segment URLs
// to fetch+concatenate and the file extension the resulting bytes should have.
type StreamInfo struct {
	URLs      []string
	Extension string // ".flac" or ".m4a"
	Codec     string // raw codec string, e.g. "flac", "mp4a.40.2", "eac3"
	Dolby     bool   // true for Dolby Atmos codecs (eac3/ac4)
}

// ParseTrackStream decodes a TrackStream's base64 manifest into segment URLs and
// a file extension, handling both manifest formats Tidal uses. Mirrors tiddl's
// core/utils/parse.py parse_track_stream. There is no decryption: modern Tidal
// FLAC is served as plain (unencrypted) segment URLs.
func ParseTrackStream(ts TrackStream) (StreamInfo, error) {
	decoded, err := base64.StdEncoding.DecodeString(ts.Manifest)
	if err != nil {
		return StreamInfo{}, fmt.Errorf("decoding manifest: %w", err)
	}

	var urls []string
	var codecs string

	switch ts.ManifestMimeType {
	case "application/vnd.tidal.bts":
		var m btsManifest
		if err := json.Unmarshal(decoded, &m); err != nil {
			return StreamInfo{}, fmt.Errorf("parsing bts manifest: %w", err)
		}
		urls, codecs = m.URLs, m.Codecs
	case "application/dash+xml":
		urls, codecs, err = parseDashManifest(decoded)
		if err != nil {
			return StreamInfo{}, err
		}
	default:
		return StreamInfo{}, fmt.Errorf("unknown manifestMimeType %q", ts.ManifestMimeType)
	}

	ext, err := extensionFor(codecs, ts.AudioQuality)
	if err != nil {
		return StreamInfo{}, fmt.Errorf("track %d: %w", ts.TrackID, err)
	}
	return StreamInfo{URLs: urls, Extension: ext, Codec: codecs, Dolby: dolbyCodecs[codecs]}, nil
}

// extensionFor decides the container extension from the codec and quality,
// matching tiddl's logic: FLAC → .flac (except HI_RES_LOSSLESS FLAC-in-mp4 →
// .m4a which is later remuxed), mp4/Dolby → .m4a.
func extensionFor(codecs, audioQuality string) (string, error) {
	switch {
	case codecs == "flac":
		if audioQuality == "HI_RES_LOSSLESS" {
			return ".m4a", nil // FLAC inside mp4; remuxed to .flac after download
		}
		return ".flac", nil
	case strings.HasPrefix(codecs, "mp4") || dolbyCodecs[codecs]:
		return ".m4a", nil
	default:
		return "", fmt.Errorf("unknown codecs %q", codecs)
	}
}

// --- DASH XML manifest (used for true HiRes masters) ---

type mpd struct {
	Period struct {
		AdaptationSet struct {
			Representation struct {
				Codecs          string `xml:"codecs,attr"`
				SegmentTemplate struct {
					Media           string `xml:"media,attr"`
					SegmentTimeline struct {
						S []struct {
							R string `xml:"r,attr"`
						} `xml:"S"`
					} `xml:"SegmentTimeline"`
				} `xml:"SegmentTemplate"`
			} `xml:"Representation"`
		} `xml:"AdaptationSet"`
	} `xml:"Period"`
}

// parseDashManifest expands a DASH SegmentTemplate/SegmentTimeline into numbered
// segment URLs, mirroring tiddl's parse_manifest_XML.
func parseDashManifest(data []byte) ([]string, string, error) {
	var m mpd
	if err := xml.Unmarshal(data, &m); err != nil {
		return nil, "", fmt.Errorf("parsing dash manifest: %w", err)
	}
	rep := m.Period.AdaptationSet.Representation
	tmpl := rep.SegmentTemplate.Media
	if tmpl == "" {
		return nil, "", fmt.Errorf("no media template in dash manifest")
	}

	// Total segment count = number of <S> entries plus their repeat (r) counts.
	total := 0
	for _, s := range rep.SegmentTemplate.SegmentTimeline.S {
		total++
		if s.R != "" {
			if r, err := strconv.Atoi(s.R); err == nil {
				total += r
			}
		}
	}
	if total == 0 {
		return nil, "", fmt.Errorf("empty segment timeline")
	}

	urls := make([]string, 0, total+1)
	for i := 0; i <= total; i++ {
		urls = append(urls, strings.ReplaceAll(tmpl, "$Number$", strconv.Itoa(i)))
	}
	return urls, rep.Codecs, nil
}
