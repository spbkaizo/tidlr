package tidal

import (
	"strings"
	"time"
)

// Artist is a Tidal artist reference embedded in album responses.
type Artist struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Album is the subset of Tidal's album object that tidlr needs.
type Album struct {
	ID             int64    `json:"id"`
	Title          string   `json:"title"`
	Artist         Artist   `json:"artist"`
	Artists        []Artist `json:"artists"`
	AudioModes     []string `json:"audioModes"`
	AudioQuality   string   `json:"audioQuality"`
	Explicit       bool     `json:"explicit"`
	NumberOfTracks int      `json:"numberOfTracks"`
	ReleaseDate    string   `json:"releaseDate"` // "YYYY-MM-DD"
	Cover          string   `json:"cover"`       // image UUID
}

// ArtistName returns the primary artist name, preferring the singular artist
// field and falling back to the first entry of artists[].
func (a Album) ArtistName() string {
	if a.Artist.Name != "" {
		return a.Artist.Name
	}
	if len(a.Artists) > 0 {
		return a.Artists[0].Name
	}
	return ""
}

// ArtistID returns the primary artist id (0 if unknown).
func (a Album) ArtistID() int64 {
	if a.Artist.ID != 0 {
		return a.Artist.ID
	}
	if len(a.Artists) > 0 {
		return a.Artists[0].ID
	}
	return 0
}

// HasMode reports whether the album exposes the given audio mode (e.g. STEREO).
func (a Album) HasMode(mode string) bool {
	for _, m := range a.AudioModes {
		if strings.EqualFold(m, mode) {
			return true
		}
	}
	return false
}

// PrimaryMode returns the first audio mode, or "UNKNOWN".
func (a Album) PrimaryMode() string {
	if len(a.AudioModes) > 0 {
		return a.AudioModes[0]
	}
	return "UNKNOWN"
}

// ReleaseTime parses ReleaseDate; returns the zero time if absent/unparseable.
func (a Album) ReleaseTime() time.Time {
	t, err := time.Parse("2006-01-02", a.ReleaseDate)
	if err != nil {
		return time.Time{}
	}
	return t
}

// SearchResult is the shape of GET /search that we consume (albums only).
type SearchResult struct {
	Albums struct {
		Items []Album `json:"items"`
	} `json:"albums"`
}

// ArtistAlbums is the paginated result of GET /artists/{id}/albums.
type ArtistAlbums struct {
	Limit              int     `json:"limit"`
	Offset             int     `json:"offset"`
	TotalNumberOfItems int     `json:"totalNumberOfItems"`
	Items              []Album `json:"items"`
}

// TrackAlbum is the album reference embedded in a track (present on playlist
// items), carrying enough to tag and fetch cover art.
type TrackAlbum struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Cover       string `json:"cover"`
	ReleaseDate string `json:"releaseDate"`
}

// Track is a single track. Within album items only the core fields are set;
// playlist items additionally populate Artist/Artists/Album.
type Track struct {
	ID           int64      `json:"id"`
	Title        string     `json:"title"`
	TrackNumber  int        `json:"trackNumber"`
	VolumeNumber int        `json:"volumeNumber"`
	Duration     int        `json:"duration"`
	Explicit     bool       `json:"explicit"`
	Artist       Artist     `json:"artist"`
	Artists      []Artist   `json:"artists"`
	Album        TrackAlbum `json:"album"`
}

// ArtistName returns the track's primary artist name.
func (t Track) ArtistName() string {
	if t.Artist.Name != "" {
		return t.Artist.Name
	}
	if len(t.Artists) > 0 {
		return t.Artists[0].Name
	}
	return ""
}

// AlbumItems is the paginated track listing of GET /albums/{id}/items. Each
// entry wraps the actual track under "item".
type AlbumItems struct {
	Limit              int `json:"limit"`
	Offset             int `json:"offset"`
	TotalNumberOfItems int `json:"totalNumberOfItems"`
	Items              []struct {
		Item Track  `json:"item"`
		Type string `json:"type"`
	} `json:"items"`
}

// Playlist is the metadata of GET /playlists/{uuid}.
type Playlist struct {
	UUID           string `json:"uuid"`
	Title          string `json:"title"`
	NumberOfTracks int    `json:"numberOfTracks"`
}

// PlaylistItems is the paginated track listing of GET /playlists/{uuid}/items.
type PlaylistItems struct {
	Limit              int `json:"limit"`
	Offset             int `json:"offset"`
	TotalNumberOfItems int `json:"totalNumberOfItems"`
	Items              []struct {
		Item Track  `json:"item"`
		Type string `json:"type"`
	} `json:"items"`
}

// TrackStream is the playback info for a track: a base64 manifest describing
// the segment URLs and codec.
type TrackStream struct {
	TrackID          int64  `json:"trackId"`
	AudioQuality     string `json:"audioQuality"`
	ManifestMimeType string `json:"manifestMimeType"`
	Manifest         string `json:"manifest"` // base64
}
