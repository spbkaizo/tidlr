package tidal

import (
	"context"
	"strconv"
)

// Search runs a Tidal catalogue search and returns album hits.
func (c *Client) Search(ctx context.Context, query string) (SearchResult, error) {
	var res SearchResult
	err := c.get(ctx, "search", map[string]string{
		"countryCode": c.countryCode(),
		"query":       query,
	}, &res)
	return res, err
}

// GetAlbum fetches a single album by id.
func (c *Client) GetAlbum(ctx context.Context, albumID int64) (Album, error) {
	var al Album
	err := c.get(ctx, "albums/"+strconv.FormatInt(albumID, 10), map[string]string{
		"countryCode": c.countryCode(),
	}, &al)
	return al, err
}

// GetArtistAlbums fetches one page of an artist's albums. The API caps limit at
// 100; callers page via offset.
func (c *Client) GetArtistAlbums(ctx context.Context, artistID int64, limit, offset int) (ArtistAlbums, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var res ArtistAlbums
	err := c.get(ctx, "artists/"+strconv.FormatInt(artistID, 10)+"/albums", map[string]string{
		"countryCode": c.countryCode(),
		"limit":       strconv.Itoa(limit),
		"offset":      strconv.Itoa(offset),
		"filter":      "ALBUMS",
	}, &res)
	return res, err
}

// GetAlbumItems fetches one page of an album's track listing (max 100 per call).
func (c *Client) GetAlbumItems(ctx context.Context, albumID int64, limit, offset int) (AlbumItems, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var res AlbumItems
	err := c.get(ctx, "albums/"+strconv.FormatInt(albumID, 10)+"/items", map[string]string{
		"countryCode": c.countryCode(),
		"limit":       strconv.Itoa(limit),
		"offset":      strconv.Itoa(offset),
	}, &res)
	return res, err
}

// GetTrack fetches a single track's metadata by id. The response carries the
// track's own album, so a standalone track has everything needed for tagging.
func (c *Client) GetTrack(ctx context.Context, trackID int64) (Track, error) {
	var t Track
	err := c.get(ctx, "tracks/"+strconv.FormatInt(trackID, 10), map[string]string{
		"countryCode": c.countryCode(),
	}, &t)
	return t, err
}

// GetTrackStream fetches playback info (the manifest) for a track at the given
// quality (LOW, HIGH, LOSSLESS, HI_RES_LOSSLESS).
//
// immersiveaudio=false forces the STEREO stream. Without it, albums that have a
// Dolby Atmos edition return lossy eac3 5.1 even when a lossless stereo master
// exists; with it we get proper stereo FLAC (or AAC at lower qualities), which
// is what a normal music library wants.
func (c *Client) GetTrackStream(ctx context.Context, trackID int64, quality string) (TrackStream, error) {
	var ts TrackStream
	err := c.get(ctx, "tracks/"+strconv.FormatInt(trackID, 10)+"/playbackinfopostpaywall", map[string]string{
		"audioquality":      quality,
		"playbackmode":      "STREAM",
		"assetpresentation": "FULL",
		"immersiveaudio":    "false",
	}, &ts)
	return ts, err
}

// GetPlaylist fetches a playlist's metadata by uuid.
func (c *Client) GetPlaylist(ctx context.Context, uuid string) (Playlist, error) {
	var p Playlist
	err := c.get(ctx, "playlists/"+uuid, map[string]string{
		"countryCode": c.countryCode(),
	}, &p)
	return p, err
}

// GetPlaylistItems fetches one page of a playlist's tracks (max 100 per call).
func (c *Client) GetPlaylistItems(ctx context.Context, uuid string, limit, offset int) (PlaylistItems, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var res PlaylistItems
	err := c.get(ctx, "playlists/"+uuid+"/items", map[string]string{
		"countryCode": c.countryCode(),
		"limit":       strconv.Itoa(limit),
		"offset":      strconv.Itoa(offset),
	}, &res)
	return res, err
}
