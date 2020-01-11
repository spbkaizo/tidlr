package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	// TODO(ts): look at replacing bitio

	"github.com/icza/bitio"
	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/meta"
)

const baseurl = "https://api.tidalhifi.com/v1/"
const clientVersion = "1.9.1"

// See around https://github.com/arnesongit/plugin.audio.tidal2/blob/master/resources/lib/koditidal.py#L779
// For tokens that are valid...
var token string

const atoken = "kgsOOmYk3zShYrNP" // This is the Android token *nb: All Streams are HTTP Streams. Correct numberOfVideos in Playlists (best Token to use)
const mtoken = "MbjR4DLXz1ghC4rV" // Like Android Token, supports MQA, but returns 'numberOfVideos = 0' in Playlists
// You would be wise to peruse articles on MQA before switching, e.g.
// http://archimago.blogspot.com/2017/10/mqa-final-final-comment-simply-put-why.html#more

const (
	AQ_MQM int = iota
	AQ_LOSSLESS
	AQ_HI_RES
)

var cookieJar, _ = cookiejar.New(nil)
var c = &http.Client{
	Jar: cookieJar,
}

type TidalError struct {
	Status      int
	SubStatus   int
	UserMessage string
}

// Tidal api struct
type Tidal struct {
	albumMap    map[string]Album
	SessionID   string      `json:"sessionID"`
	CountryCode string      `json:"countryCode"`
	UserID      json.Number `json:"userId"`
}

// Artist struct
type Artist struct {
	ID   json.Number `json:"id"`
	Name string      `json:"name"`
	Type string      `json:"type"`
}

// Album struct
type Album struct {
	Artists              []Artist    `json:"artists,omitempty"`
	Title                string      `json:"title"`
	ID                   json.Number `json:"id"`
	NumberOfTracks       json.Number `json:"numberOfTracks"`
	Explicit             bool        `json:"explicit,omitempty"`
	Copyright            string      `json:"copyright,omitempty"`
	AudioQuality         string      `json:"audioQuality"`
	ReleaseDate          string      `json:"releaseDate"`
	Duration             float64     `json:"duration"`
	PremiumStreamingOnly bool        `json:"premiumStreamingOnly"`
	Popularity           float64     `json:"popularity,omitempty"`
	Artist               Artist      `json:"artist"`
	Cover                string      `json:"cover"`
	artBody              []byte
}

// Track struct
type Track struct {
	Artists        []Artist    `json:"artists"`
	Artist         Artist      `json:"artist"`
	Album          Album       `json:"album"`
	Title          string      `json:"title"`
	ID             json.Number `json:"id"`
	Explicit       bool        `json:"explicit"`
	Copyright      string      `json:"copyright"`
	Popularity     int         `json:"popularity"`
	TrackNumber    json.Number `json:"trackNumber"`
	Duration       json.Number `json:"duration"`
	AudioQuality   string      `json:"audioQuality"`
	PartOfPlaylist bool        // extension to indicate a playlist for flow
}

// Search struct
type Search struct {
	Items  []Album `json:"items"`
	Albums struct {
		Items []Album `json:"items"`
	} `json:"albums"`
	Artists struct {
		Items []Artist `json:"items"`
	} `json:"artists"`
	Tracks struct {
		Items []Track `json:"items"`
	} `json:"tracks"`
}

type PlaylistInfo struct {
	Created string `json:"created"`
	Creator struct {
		ID int64 `json:"id"`
	} `json:"creator"`
	Description    string `json:"description"`
	Duration       int64  `json:"duration"`
	Image          string `json:"image"`
	LastUpdated    string `json:"lastUpdated"`
	NumberOfTracks int64  `json:"numberOfTracks"`
	NumberOfVideos int64  `json:"numberOfVideos"`
	Popularity     int64  `json:"popularity"`
	PublicPlaylist bool   `json:"publicPlaylist"`
	SquareImage    string `json:"squareImage"`
	Title          string `json:"title"`
	Type           string `json:"type"`
	URL            string `json:"url"`
	Uuid           string `json:"uuid"`
}

type PlaylistItems struct {
	Items []struct {
		Cut  interface{} `json:"cut"`
		Item struct {
			ID    int64 `json:"id"`
			Album struct {
				ID          int64  `json:"id"`
				Cover       string `json:"cover"`
				ReleaseDate string `json:"releaseDate"`
				Title       string `json:"title"`
			} `json:"album"`
			AllowStreaming bool `json:"allowStreaming"`
			Artist         struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"artist"`
			Artists []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"artists"`
			AudioModes           []string    `json:"audioModes"`
			AudioQuality         string      `json:"audioQuality"`
			Copyright            string      `json:"copyright"`
			Description          interface{} `json:"description"`
			Duration             int64       `json:"duration"`
			Editable             bool        `json:"editable"`
			Explicit             bool        `json:"explicit"`
			Isrc                 string      `json:"isrc"`
			Peak                 float64     `json:"peak"`
			Popularity           int64       `json:"popularity"`
			PremiumStreamingOnly bool        `json:"premiumStreamingOnly"`
			ReplayGain           float64     `json:"replayGain"`
			StreamReady          bool        `json:"streamReady"`
			StreamStartDate      string      `json:"streamStartDate"`
			Title                string      `json:"title"`
			TrackNumber          int64       `json:"trackNumber"`
			URL                  string      `json:"url"`
			Version              interface{} `json:"version"`
			VolumeNumber         int64       `json:"volumeNumber"`
		} `json:"item"`
		Type string `json:"type"`
	} `json:"items"`
	Limit              int64 `json:"limit"`
	Offset             int64 `json:"offset"`
	TotalNumberOfItems int64 `json:"totalNumberOfItems"`
}

func (t *Tidal) get(dest string, query *url.Values, s interface{}) error {
	//log.Println(baseurl+dest+"?"+query.Encode(), t.SessionID)
	req, err := http.NewRequest("GET", baseurl+dest, nil)
	if err != nil {
		return err
	}
	req.Header.Add("X-Tidal-SessionID", t.SessionID)
	query.Add("countryCode", t.CountryCode)
	req.URL.RawQuery = query.Encode()
	//log.Printf("DEBUG: %v", req.URL.RawQuery)
	res, err := c.Do(req)
	if err != nil {
		return err
	}

	defer res.Body.Close()
	//body, err := ioutil.ReadAll(res.Body)
	//log.Printf("DEBUG: %v", string(body))
	return json.NewDecoder(res.Body).Decode(&s)
}

func (t *Tidal) CheckSession() (bool, error) {
	//if self.user is None or not self.user.id or not self.session_id:
	//return False
	var out interface{}
	err := t.get(fmt.Sprintf("users/%s/subscription", t.UserID), nil, &out)
	// fmt.Println(out)
	return true, err
}

// TODO - change function to return an array of structs
func (t *Tidal) GetFavoriteAlbums() ([]string, error) {
	var out struct {
		Limit              int `json:"limit"`
		Offset             int `json:"offset"`
		TotalNumberOfItems int `json:"totalNumberOfItems"`
		Items              []struct {
			Created string `json:"created"`
			Item    struct {
				ID                   int         `json:"id"`
				Title                string      `json:"title"`
				Duration             int         `json:"duration"`
				StreamReady          bool        `json:"streamReady"`
				StreamStartDate      string      `json:"streamStartDate"`
				AllowStreaming       bool        `json:"allowStreaming"`
				PremiumStreamingOnly bool        `json:"premiumStreamingOnly"`
				NumberOfTracks       int         `json:"numberOfTracks"`
				NumberOfVideos       int         `json:"numberOfVideos"`
				NumberOfVolumes      int         `json:"numberOfVolumes"`
				ReleaseDate          string      `json:"releaseDate"`
				Copyright            string      `json:"copyright"`
				Type                 string      `json:"type"`
				Version              interface{} `json:"version"`
				URL                  string      `json:"url"`
				Cover                string      `json:"cover"`
				VideoCover           interface{} `json:"videoCover"`
				Explicit             bool        `json:"explicit"`
				Upc                  string      `json:"upc"`
				Popularity           int         `json:"popularity"`
				AudioQuality         string      `json:"audioQuality"`
				SurroundTypes        interface{} `json:"surroundTypes"`
				Artist               struct {
					ID   int    `json:"id"`
					Name string `json:"name"`
					Type string `json:"type"`
				} `json:"artist"`
				Artists []struct {
					ID   int    `json:"id"`
					Name string `json:"name"`
					Type string `json:"type"`
				} `json:"artists"`
			} `json:"item"`
		} `json:"items"`
	}

	err := t.get(fmt.Sprintf("users/%s/favorites/albums", t.UserID), &url.Values{
		"limit": {"500"},
	}, &out)
	var ids []string

	for _, id := range out.Items {
		ids = append(ids, strconv.Itoa(id.Item.ID))
	}

	return ids, err
}

// GetStreamURL func
func (t *Tidal) GetStreamURL(id, q string) (string, error) {
	var s struct {
		URL string `json:"url"`
	}
	err := t.get("tracks/"+id+"/streamUrl", &url.Values{
		"soundQuality": {q},
	}, &s)

	// fmt.Println(s.URL)

	return s.URL, err
}

func (t *Tidal) GetAlbum(id string) (Album, error) {
	var s Album

	if album, ok := t.albumMap[id]; ok {
		return album, nil
	}

	err := t.get("albums/"+id, &url.Values{}, &s)
	t.albumMap[id] = s

	if s.Duration == 0 {
		return s, errors.New("album unavailable")
	}

	return s, err
}

// GetAlbumTracks func
func (t *Tidal) GetAlbumTracks(id string) ([]Track, error) {
	var s struct {
		Items []Track `json:"items"`
	}
	return s.Items, t.get("albums/"+id+"/tracks", &url.Values{}, &s)
}

// GetPlaylistInfo func
func (t *Tidal) GetPlaylistInfo(id string) (PlaylistInfo, error) {
	var plistinfo PlaylistInfo
	return plistinfo, t.get("playlists/"+id, &url.Values{}, &plistinfo)
}

// GetPlaylistTracks func
func (t *Tidal) GetPlaylistTracks(id string) (PlaylistInfo, []Track, error) {
	var s struct {
		PlaylistInfo PlaylistInfo
		Items        []Track `json:"items"`
	}
	var err error
	s.PlaylistInfo, err = t.GetPlaylistInfo(id)
	if err != nil {
		log.Printf("ERROR: While getting Playlist Information (%v)", err)
	}
	//log.Printf("DEBUG: %v", s.PlaylistInfo)
	//return s.Items, t.get("playlists/"+id+"/tracks", &url.Values{"limit": {"1000"}}, &s)
	return s.PlaylistInfo, s.Items, t.get("playlists/"+id+"/tracks", &url.Values{"limit": {"1000"}}, &s)
}

// SearchTracks func
func (t *Tidal) SearchTracks(d string, l int) ([]Track, error) {
	var s Search
	var limit string

	if l > 0 {
		limit = strconv.Itoa(l)
	}

	return s.Tracks.Items, t.get("search", &url.Values{
		"query": {d},
		"types": {"TRACKS"},
		"limit": {limit},
	}, &s)
}

// SearchAlbums func
func (t *Tidal) SearchAlbums(d string, l int) ([]Album, error) {
	var s Search
	var limit string

	if l > 0 {
		limit = strconv.Itoa(l)
	}

	err := t.get("search", &url.Values{
		"query": {d},
		"types": {"ALBUMS"},
		"limit": {limit},
	}, &s)

	if err != nil {
		return s.Albums.Items, err
	}

	for _, album := range s.Albums.Items {
		t.albumMap[album.ID.String()] = album
	}

	return s.Albums.Items, nil
}

// SearchArtists func
func (t *Tidal) SearchArtists(d string, l int) ([]Artist, error) {
	var s Search
	var limit string

	if l > 0 {
		limit = strconv.Itoa(l)
	}

	return s.Artists.Items, t.get("search", &url.Values{
		"query": {d},
		"types": {"ARTISTS"},
		"limit": {limit},
	}, &s)
}

func (t *Tidal) GetArtist(artist string) (Artist, error) {
	var s Artist
	err := t.get(fmt.Sprintf("artists/%s", artist), &url.Values{}, &s)
	return s, err
}

// GetArtistAlbums func
func (t *Tidal) GetArtistAlbums(artist string, l int) ([]Album, error) {
	var s Search
	var limit string

	if l > 0 {
		limit = strconv.Itoa(l)
	}

	err := t.get(fmt.Sprintf("artists/%s/albums", artist), &url.Values{
		"limit": {limit},
	}, &s)

	if err != nil {
		return s.Items, err
	}

	for _, album := range s.Items {
		t.albumMap[album.ID.String()] = album
	}

	return s.Items, nil
}

func (t *Tidal) GetArtistEP(artist string, l int) ([]Album, error) {
	var s Search
	var limit string

	if l > 0 {
		limit = strconv.Itoa(l)
	}

	err := t.get(fmt.Sprintf("artists/%s/albums", artist), &url.Values{
		"limit":  {limit},
		"filter": {"EPSANDSINGLES"},
	}, &s)

	if err != nil {
		return s.Items, err
	}

	for _, album := range s.Items {
		t.albumMap[album.ID.String()] = album
	}

	return s.Items, nil
}

func (al *Album) GetArt() ([]byte, error) {
	u := "https://resources.tidal.com/images/" + strings.Replace(al.Cover, "-", "/", -1) + "/1280x1280.jpg"
	res, err := http.Get(u)
	if err != nil {
		return nil, err
	}

	defer res.Body.Close()
	return ioutil.ReadAll(res.Body)
}

func (t *Tidal) DownloadAlbum(al Album) error {
	tracks, err := t.GetAlbumTracks(al.ID.String())
	if err != nil {
		return err
	}

	if al.Duration == 0.0 {
		return errors.New("album unavailable")
	}

	dirs := clean(al.Artists[0].Name) + " - " + clean(al.Title)
	os.MkdirAll(dirs, os.ModePerm)

	metadata, err := json.MarshalIndent(al, "", "\t")
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(dirs+"/meta.json", metadata, 0777)
	if err != nil {
		return err
	}

	body, err := al.GetArt()
	if err != nil {
		return err
	}

	al.artBody = body
	t.albumMap[al.ID.String()] = al

	err = ioutil.WriteFile(dirs+"/album.jpg", body, 0777)
	if err != nil {
		return err
	}

	for i, track := range tracks {
		log.Printf("\t [%v/%v] %v\n", i+1, len(tracks), track.Title)
		//log.Printf("DEBUG track is %v", track)
		var plist PlaylistInfo // not needed
		if err := t.DownloadTrack(plist, track); err != nil {
			return err
		}
	}

	return nil
}

func (t *Tidal) DownloadPlayList(id string) error {
	//log.Printf("Playlist ID: %v", id)
	plist, tracks, err := t.GetPlaylistTracks(id)
	if err != nil {
		//log.Printf("ERROR: %v", err)
		return err
	}

	//log.Printf("Total tracks %v, Tracks %v", len(tracks), tracks)
	log.Printf("INFO: Downloading %v tracks from Playlist %v", len(tracks), id)
	var tcount int
	for _, track := range tracks {
		tcount++
		track.PartOfPlaylist = true
		log.Printf("INFO: Downloading %v - %v [%v/%v]", track.Artist.Name, track.Title, tcount, len(tracks))

		track.TrackNumber = json.Number(strconv.Itoa(tcount)) // weird.
		if err != nil {
			log.Printf("ERROR: %v")
		}
		err = t.DownloadTrack(plist, track)
		if err != nil {
			return err
		}
		//log.Printf("TRACK: %v", track)
	}

	dirs := clean(plist.Title)
	os.MkdirAll(dirs, os.ModePerm)

	metadata, err := json.MarshalIndent(plist, "", "\t")
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(dirs+"/meta.json", metadata, 0777)
	if err != nil {
		return err
	}

	return nil
}

func (t *Tidal) DownloadTrack(plist PlaylistInfo, tr Track) error {
	time.Sleep(667 * time.Millisecond) // seems to help with API errors... rate throttling?
	if tr.Explicit == true && *onlyClean == true {
		log.Printf("INFO: Skipping    %v - %v (Explicit)", tr.Artist.Name, tr.Title)
		return nil
	}

	// TODO(ts): improve ID3
	al := t.albumMap[tr.Album.ID.String()]
	tr.Album = al

	if tr.PartOfPlaylist == true {
		tr.Album.Title = plist.Title
		tr.Album.Artist.Name = "Tidal"
	}

	// path := src + "/" + tr.TrackNumber.String() + " - " + clean(tr.Artist.Name) + " - " + clean(tr.Title)
	var tracknum string
	//if tr.PartOfPlaylist == false {
	tint, _ := tr.TrackNumber.Int64()
	if tint < 10 {
		tracknum = "0" + tr.TrackNumber.String()
	} else {
		tracknum = tr.TrackNumber.String()
	}
	//} else {
	//	tracknum = "00"
	//}

	u, err := t.GetStreamURL(tr.ID.String(), "LOSSLESS")
	if err != nil {
		return err
	}

	if u != "" {
		res, err := http.Get(u)
		if err != nil {
			return err
		}
		var dirs string

		if !tr.PartOfPlaylist {
			dirs = clean(al.Artist.Name) + " - " + clean(al.Title)
		} else {
			dirs = clean(plist.Title)
		}
		os.MkdirAll(dirs, os.ModePerm)
		//    path := src + "/" + tracknum + " - " + clean(tr.Artist.Name) + " - " + clean(tr.Title)
		path := dirs + "/" + tracknum + " - " + clean(tr.Artist.Name) + " - " + clean(tr.Title)

		_, err = os.Stat("./" + path + ".flac")
		if !os.IsNotExist(err) {
			return nil
		}

		f, err := os.Create(path)
		if err != nil {
			return err
		}
		io.Copy(f, res.Body)
		res.Body.Close()
		f.Close()

		err = enc(dirs, tr)
		if err != nil {
			if strings.Contains(err.Error(), "flac.parseStreamInfo: invalid FLAC signature; expected") {
				// this isn't a flac file.  return
				log.Printf("ERROR: File %v isn't a FLAC file, removing and continuing.", path)
			} else {
				return err
			}
		}
		os.Remove(path)
	}
	return nil
}

// helper function to generate a uuid
func uuid() string {
	b := make([]byte, 16)
	rand.Read(b[:])
	b[8] = (b[8] | 0x40) & 0x7F
	b[6] = (b[6] & 0xF) | (4 << 4)
	return fmt.Sprintf("%x", b)
}

// New func
func New(user, pass string) (*Tidal, error) {
	query := url.Values{
		"username":        {user},
		"password":        {pass},
		"token":           {token},
		"clientUniqueKey": {uuid()},
		"clientVersion":   {clientVersion},
	}
	res, err := http.PostForm(baseurl+"login/username", query)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected error code from tidal: %d", res.StatusCode)
	}

	defer res.Body.Close()
	var t Tidal
	t.albumMap = make(map[string]Album)
	return &t, json.NewDecoder(res.Body).Decode(&t)
}

func clean(s string) string {
	s = strings.Replace(s, ":", "-", -1)
	s = strings.Replace(s, "\"", "~", -1)
	return strings.Replace(s, "/", "\u2215", -1)
}

func enc(src string, tr Track) error {
	// https://wiki.hydrogenaud.io/index.php?title=Tag_Mapping#Titles
	// Decode FLAC file.
	var tracknum string
	//if tr.PartOfPlaylist == false {
	tint, _ := tr.TrackNumber.Int64()
	if tint < 10 {
		tracknum = "0" + tr.TrackNumber.String()
	} else {
		tracknum = tr.TrackNumber.String()
	}
	//} else {
	//	tracknum = "00"
	//}
	path := src + "/" + tracknum + " - " + clean(tr.Artist.Name) + " - " + clean(tr.Title)
	stream, err := flac.ParseFile(path)
	if err != nil {
		// this might be an AAC file, check
		return err
	}

	// https://xiph.org/flac/format.html#metadata_block_picture
	MIMETYPE := "image/jpeg"
	pictureData := &bytes.Buffer{}
	w := bitio.NewWriter(pictureData)
	w.WriteBits(uint64(3), 32)                     // picture type (3)
	w.WriteBits(uint64(len(MIMETYPE)), 32)         // length of "image/jpeg"
	w.Write([]byte(MIMETYPE))                      // "image/jpeg"
	w.WriteBits(uint64(0), 32)                     // description length (0)
	w.Write([]byte{})                              // description
	w.WriteBits(uint64(1280), 32)                  // width (1280)
	w.WriteBits(uint64(1280), 32)                  // height (1280)
	w.WriteBits(uint64(24), 32)                    // colour depth (24)
	w.WriteBits(uint64(0), 32)                     // is pal? (0)
	w.WriteBits(uint64(len(tr.Album.artBody)), 32) // length of content
	w.Write(tr.Album.artBody)                      // actual content
	w.Close()

	encodedPictureData := base64.StdEncoding.EncodeToString(pictureData.Bytes())
	_ = encodedPictureData

	foundComments := false

	comments := [][2]string{}
	comments = append(comments, [2]string{"TITLE", tr.Title})
	comments = append(comments, [2]string{"ALBUM", tr.Album.Title})
	comments = append(comments, [2]string{"TRACKNUMBER", tr.TrackNumber.String()})
	comments = append(comments, [2]string{"TRACKTOTAL", tr.Album.NumberOfTracks.String()})
	comments = append(comments, [2]string{"ARTIST", tr.Artist.Name})
	comments = append(comments, [2]string{"ALBUMARTIST", tr.Album.Artist.Name})
	comments = append(comments, [2]string{"COPYRIGHT", tr.Copyright})
	comments = append(comments, [2]string{"METADATA_BLOCK_PICTURE", encodedPictureData})
	comments = append(comments, [2]string{"DATE", tr.Album.ReleaseDate}) // Should help MusicBrainz Identification
	// comments = append(comments, [2]string{"ReplayGain Reference Loudness"})

	// Add custom vorbis comment.
	for _, block := range stream.Blocks {
		if comment, ok := block.Body.(*meta.VorbisComment); ok {
			foundComments = true
			comment.Tags = append(comment.Tags, comments...)
		}
	}

	if foundComments == false {
		block := new(meta.Block)
		block.IsLast = true
		block.Type = meta.Type(4)
		block.Length = 0

		comment := new(meta.VorbisComment)
		block.Body = comment
		comment.Vendor = "Lavf57.71.100"
		comment.Tags = append(comment.Tags, comments...)

		stream.Blocks = append(stream.Blocks, block)
	}

	// Encode FLAC file.
	f, err := os.Create(path + ".flac")
	if err != nil {
		return err
	}
	err = flac.Encode(f, stream)
	//enc, err := flac.NewEncoder(f, comments, stream.Blocks)
	f.Close()
	stream.Close()
	return err
}
