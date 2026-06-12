package exifutil

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	"github.com/filerename/filerename/pkg/types"
)

var imageExts = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  false,
	".gif":  false,
	".tiff": true,
	".tif":  true,
	".heic": false,
	".heif": false,
	".bmp":  false,
	".webp": false,
	".raw":  true,
	".cr2":  true,
	".cr3":  true,
	".nef":  true,
	".arw":  true,
	".dng":  true,
}

func SupportsExif(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return imageExts[ext]
}

func ExtractDate(path string) (*time.Time, error) {
	if !SupportsExif(path) {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	x, err := exif.Decode(f)
	if err != nil {
		return nil, nil
	}

	t, err := x.DateTime()
	if err != nil {
		if dtTag, e := x.Get(exif.DateTimeOriginal); e == nil {
			s, e := dtTag.StringVal()
			if e == nil {
				if parsed, pe := parseExifTime(s); pe == nil {
					return &parsed, nil
				}
			}
		}
		if dtTag, e := x.Get(exif.DateTimeDigitized); e == nil {
			s, e := dtTag.StringVal()
			if e == nil {
				if parsed, pe := parseExifTime(s); pe == nil {
					return &parsed, nil
				}
			}
		}
		return nil, nil
	}
	return &t, nil
}

func parseExifTime(s string) (time.Time, error) {
	layouts := []string{
		"2006:01:02 15:04:05",
		"2006-01-02 15:04:05",
		"2006:01:02T15:04:05",
		"2006:01:02",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, nil
}

func BatchExtractExif(files []*types.FileInfo, workers int) {
	if workers <= 0 {
		workers = 8
	}
	sem := make(chan struct{}, workers)
	done := make(chan struct{})
	go func() {
		for _, f := range files {
			f := f
			sem <- struct{}{}
			go func() {
				defer func() { <-sem }()
				if t, err := ExtractDate(f.Path); err == nil && t != nil {
					f.ExifDate = t
				}
			}()
		}
		for i := 0; i < workers; i++ {
			sem <- struct{}{}
		}
		close(done)
	}()
	<-done
}
