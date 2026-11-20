package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/simonb/tidlr/internal/adm"
	"github.com/simonb/tidlr/internal/config"
	"github.com/simonb/tidlr/internal/convert"
	"github.com/simonb/tidlr/internal/downloader"
	"github.com/simonb/tidlr/internal/fsname"
	"github.com/simonb/tidlr/internal/tidal"
)

const (
	usbReserve   = 200 << 20 // stop when less than this is free
	usbMaxMisses = 25        // consecutive albums that don't fit before giving up
)

// mustUSB fills the folder dest (a mounted stick) with 192k MP3 copies of the
// ADM chart releases added on/after since (the "Just in" page when since is
// zero), newest first, skipping albums that cannot be matched on Tidal or no
// longer fit, until the stick is full.
func mustUSB(ctx context.Context, cfg config.Config, dest string, since time.Time) {
	if info, err := os.Stat(dest); err != nil || !info.IsDir() {
		log.Fatalf("usb: %s is not a directory", dest)
	}
	client := adm.New()
	var chart []adm.Release
	var err error
	if since.IsZero() {
		chart, err = client.JustOut(ctx)
	} else {
		chart, err = client.Since(ctx, since, 0)
	}
	if err != nil || len(chart) == 0 {
		log.Fatalf("usb: no releases to copy: %v", err)
	}
	fillUSB(ctx, cfg, dest, chart)
}

// fillUSB copies each release in order, until the stick is full.
func fillUSB(ctx context.Context, cfg config.Config, dest string, chart []adm.Release) {

	tclient := authedClient(ctx, cfg)
	scratch := filepath.Join(cfg.DownloadDir(), "usbfill")
	dl := &downloader.Native{
		Matcher:    &tidal.Matcher{Client: tclient},
		Downloader: &tidal.Downloader{Client: tclient, FFmpegBin: cfg.FFmpegBin, Threads: cfg.DownloadThreads},
		Quality:    cfg.Quality,
		BaseDir:    scratch,
	}
	enc := &convert.MP3{FFmpegBin: cfg.FFmpegBin, Bitrate: "192k"}

	index, err := os.OpenFile(filepath.Join(dest, "INDEX.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatalf("usb: %v", err)
	}
	defer index.Close()
	idx := bufio.NewWriter(index)
	defer idx.Flush()

	done, misses := 0, 0
	for i, r := range chart {
		rank := i + 1
		if ctx.Err() != nil {
			break
		}
		free, err := freeBytes(dest)
		if err != nil {
			log.Fatalf("usb: %v", err)
		}
		if free < usbReserve || misses >= usbMaxMisses {
			break
		}
		albumOut := filepath.Join(dest, fsname.Sanitize(r.Artist), fsname.Sanitize(r.Album))
		if _, err := os.Stat(albumOut); err == nil {
			log.Printf("#%d already on stick: %s — %s", rank, r.Artist, r.Album)
			continue
		}

		log.Printf("#%d %s — %s (%.1f GB free)", rank, r.Artist, r.Album, float64(free)/1e9)
		res, err := dl.Download(ctx, r)
		if err != nil {
			log.Printf("#%d skipped: %v", rank, err)
			continue
		}

		// Encode off the stick first so a partial or oversized album never lands on it.
		stage := filepath.Join(scratch, "mp3", fmt.Sprint(r.ReviewID))
		os.RemoveAll(stage)
		size, err := enc.ConvertDir(ctx, res.AudioDir, stage)
		os.RemoveAll(res.AudioDir)
		if err != nil {
			log.Printf("#%d encode failed: %v", rank, err)
			os.RemoveAll(stage)
			continue
		}
		if uint64(size)+usbReserve > free {
			log.Printf("#%d does not fit (%.0f MB), trying the next", rank, float64(size)/1e6)
			os.RemoveAll(stage)
			misses++
			continue
		}
		if err := copyTree(stage, albumOut); err != nil {
			log.Printf("#%d copy to stick failed: %v", rank, err)
			os.RemoveAll(albumOut)
			os.RemoveAll(stage)
			misses++
			continue
		}
		os.RemoveAll(stage)
		misses = 0
		done++
		fmt.Fprintf(idx, "%d\t%s\t%s\t%d\n", rank, r.Artist, r.Album, size)
		idx.Flush()
		index.Sync()
	}
	free, _ := freeBytes(dest)
	log.Printf("usb: %d albums added, %.1f GB free", done, float64(free)/1e9)
	if errors.Is(ctx.Err(), context.Canceled) {
		log.Printf("usb: interrupted; rerun to resume")
	}
}

func freeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// copyTree copies the files of src (one flat album dir) into dst.
func copyTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		in, err := os.Open(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		out, err := os.Create(filepath.Join(dst, e.Name()))
		if err != nil {
			in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}
