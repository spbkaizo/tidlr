package main

import (
	//"bufio"
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"
	// "github.com/trevorstarick/tidl" // local import, only one file (tidl.go)
)

var config Config

type Config struct {
	Password string `json:"password"`
	Username string `json:"username"`
}

type History struct {
	AlbumID    int64
	Title      string
	Artist     string
	Downloaded bool
	Added      time.Time
}

var hist []History

var conf = "tidl-config.json"

var history = "tidl-history.json"
var home string

var onlyAlbums = flag.Bool("albums", true, "only download albums")
var onlyEPs = flag.Bool("eps", false, "only download eps and singles")
var onlyPlayLists = flag.Bool("playlist", false, "only download specified playlist")
var onlyClean = flag.Bool("clean", false, "only download clean(i.e. non-Explicit) tracks")
var mqa = flag.Bool("mqa", false, "Prefer MQA over FLAC Quality")

func grabSavedAlbums(t *Tidal, ids []string) error {

	for _, id := range ids {
		var albums []Album

		if id[0] == 'h' {
			id = strings.Split(id, "album/")[1]
		}

		// TODO(ts): support fetching of EP/Singles as well as flags to disable
		// TODO(ts): support fetching of artist info
		artist, err := t.GetArtist(id)
		if err != nil {
			log.Println("can't get artist info")
			return err
		}

		if artist.ID.String() != "" {
			//log.Printf("Downloading %v (%v)...\n", artist.Name, artist.ID)

			if *onlyAlbums == true {
				//log.Println("Only fetching Albums")
				lbums, err := t.GetArtistAlbums(id, 0)
				if err != nil {
					log.Println("can't get artist albums")
					os.Exit(5)
				}

				albums = append(albums, lbums...)
			} else if *onlyEPs {
				log.Println("Only fetching EPs & Singles")
				lbums, err := t.GetArtistEP(id, 0)
				if err != nil {
					log.Println("can't get artist eps")
					os.Exit(5)
				}

				albums = append(albums, lbums...)
			} else if *onlyPlayLists {
				log.Println("Fetching Playlist ID %v", artist.ID.String())
				_, tracks, err := t.GetPlaylistTracks(id)
				if err != nil {
					log.Println("can't get playlist")
					os.Exit(5)
				}
				log.Printf("DEBUG: Tracks %v", tracks)
			} else {
				log.Println("Fetching Albums, EPs & Singles")
				lbums, err := t.GetArtistAlbums(id, 0)
				if err != nil {
					log.Println("can't get artist albums")
					os.Exit(5)
				}

				albums = append(albums, lbums...)

				lbums, err = t.GetArtistEP(id, 0)
				if err != nil {
					log.Println("can't get artist eps")
					os.Exit(5)
				}

				albums = append(albums, lbums...)
			}
		} else {
			album, err := t.GetAlbum(id)
			if err != nil {
				log.Printf("can't get album info for %v (%v)", id, err)
				//os.Exit(6)
			}

			albums = []Album{album}
		}

		albumMap := make(map[string]Album)
		for _, album := range albums {
			if _, ok := albumMap[album.Title]; !ok {
				albumMap[album.Title] = album
			} else {
				// TODO(ts): impove dedupe if statement

				if album.AudioQuality == "LOSSLESS" && albumMap[album.Title].AudioQuality != "LOSSLESS" {
					// if higher quality
					albumMap[album.Title] = album
				} else if album.Explicit && !albumMap[album.Title].Explicit {
					// if explicit
					albumMap[album.Title] = album
				} else if album.Popularity > albumMap[album.Title].Popularity {
					// if more popular
					albumMap[album.Title] = album
				}
			}
		}

		albums = make([]Album, 0, len(albumMap))
		for _, album := range albumMap {
			if album.Duration > 0 {
				albums = append(albums, album)
			}
		}

		var seen bool = false
		for _, album := range albums {
			log.Printf("INFO: Found %v by %v (id: %v)\n", album.Title, album.Artist.Name, album.ID.String())
			for k, v := range hist {
				albid, _ := album.ID.Int64()
				if albid == v.AlbumID && v.Downloaded == false {
					if err := t.DownloadAlbum(album); err != nil {
						log.Printf("ERROR: Can't download album id %v [%v - %v] (%v)", albid, album.Artist.Name, album.Title, err)
						break
					}
					hist[k].Downloaded = true
					seen = true
					break
				} else if albid == v.AlbumID && v.Downloaded == true {
					seen = true
					break
				} else {
					seen = false
				}
			}
			// At this point, it was either in the history and tagged as seen, or it's a new album
			// that's been added to the favourites.
			if seen == false {
				var newhist History
				id, _ := album.ID.Int64() // never expected to fail, ymmv.
				newhist.AlbumID = id
				newhist.Title = album.Title
				newhist.Artist = album.Artist.Name
				newhist.Downloaded = false
				newhist.Added = time.Now()
				log.Printf("INFO: Attempting download of %v by %v (id: %v)\n", album.Title, album.Artist.Name, album.ID.String())
				if err := t.DownloadAlbum(album); err != nil {
					log.Printf("ERROR: Can't download album (%v)", err)
					os.Exit(8)
				} else {
					newhist.Downloaded = true // flip state, we got it ok.
				}
				hist = append(hist, newhist)
			}
		}
	}
	histjson, err := json.MarshalIndent(hist, "", "\t")
	if err != nil {
		log.Printf("ERROR: Trying to handle JSON History Data (%v)", err)
	}
	//log.Printf("%v", string(histjson))
	//log.Printf("HOME: %v", home)
	//os.Exit(0)
	err = ioutil.WriteFile(home+"/.tidl/"+history, histjson, 0644)
	if err != nil {
		log.Printf("ERROR: Writing History JSON File %v (%v)", home+"/.tidl/"+history, err)
		os.Exit(1)
	}
	return nil

}

func main() {
	/* Log better */
	log.SetFlags(log.LstdFlags | log.Ldate | log.Lmicroseconds | log.Lshortfile)

	//var hist []History
	//var history []History
	var err error
	home, err = os.UserHomeDir()
	if err != nil {
		log.Printf("ERROR: Can't figure out users home directory (%v)", err)
		os.Exit(1)
	} else {
		//log.Printf("%v", home)
		configfile, err := ioutil.ReadFile(home + "/.tidl/" + conf)
		if err != nil {
			log.Printf("ERROR: Can't read config file %v (%v)", home+"/.tidl/"+conf, err)
			os.Exit(1)
		}
		err = json.Unmarshal(configfile, &config)
		if err != nil {
			log.Fatalf("FATAL: Cannot understand the config file, appears to be malformed : %v", err)
			os.Exit(1)
		}
		if _, err = os.Stat(home + "/.tidl/" + history); !os.IsNotExist(err) {
			histbytes, err := ioutil.ReadFile(home + "/.tidl/" + history)
			if err != nil {
				log.Printf("ERROR: Can't read history file %v (%v)", home+"/.tidl/"+history, err)
				log.Fatalf("FATAL: Remove it, and try again as it is corrupted")
			}
			err = json.Unmarshal(histbytes, &hist)
			if err != nil {
				log.Printf("FATAL: Error reading history file %v (%v)", home+"/.tidl/"+history, err)
				log.Fatalf("FATAL: Remove it, and try again as it is corrupted")
			}
		}
	}

	flag.Parse()
	if *mqa == true {
		token = mtoken
	} else {
		token = atoken
	}

	t, err := New(config.Username, config.Password)
	if err != nil {
		log.Println("can't login to tidl right now")
		os.Exit(4)
	}
	log.Printf("INFO: Logged into Tidal %v, user id %v - got Session ID [%v]", t.CountryCode, t.UserID.String(), t.SessionID)

	var ids []string

	// TODO(ts): handle output better
	// TODO(ts): handle no input
	if len(flag.Args()) == 0 && *onlyPlayLists == false {
		ids, _ = t.GetFavoriteAlbums()
		/*
			for _, id := range ids {
				log.Println(id)
			}
		*/
		err = grabSavedAlbums(t, ids)
		if err != nil {
			log.Printf("ERROR: %v", err)
		}
		//os.Exit(2)
	} else if len(flag.Args()) != 0 && *onlyPlayLists == true {
		//log.Printf("here")
		// Grab Playlist
		ids = flag.Args()
		for _, id := range ids {
			if strings.Contains(id, "https://tidal.com/browse/playlist/") {
				id = strings.Replace(id, "https://tidal.com/browse/playlist/", "", -1)
			}
			//log.Printf("INFO: Retrieving playlist %v", id)
			err := t.DownloadPlayList(id)
			if err != nil {
				log.Printf("ERROR: %v", err)
			}
		}

	} else {
		ids = flag.Args() // single hit of an album, or playlist
		if len(ids) == 0 {
			log.Printf("Can't figure out what to do, not enough args given")
		} else {
			log.Printf("Meep")
		}
	}

}
