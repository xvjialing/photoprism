package photoprism

import (
	"errors"
	"fmt"
	"path"
	"runtime"

	"github.com/photoprism/photoprism/internal/config"
	"github.com/photoprism/photoprism/internal/event"
	"github.com/photoprism/photoprism/internal/mutex"
	"github.com/photoprism/photoprism/internal/query"
	"github.com/photoprism/photoprism/pkg/fs"
	"github.com/photoprism/photoprism/pkg/txt"
)

// Purge represents a worker that removes missing files from search results.
type Purge struct {
	conf *config.Config
}

// NewPurge returns a new purge worker.
func NewPurge(conf *config.Config) *Purge {
	instance := &Purge{
		conf: conf,
	}

	return instance
}

// originalsPath returns the original media files path as string.
func (prg *Purge) originalsPath() string {
	return prg.conf.OriginalsPath()
}

// Start removes missing files from search results.
func (prg *Purge) Start(opt PurgeOptions) (purgedFiles map[string]bool, purgedPhotos map[string]bool, err error) {
	defer func() {
		if err := recover(); err != nil {
			log.Errorf("purge: %s [panic]", err)
		}
	}()

	var ignore map[string]bool

	if opt.Ignore != nil {
		ignore = opt.Ignore
	} else {
		ignore = make(map[string]bool)
	}

	purgedFiles = make(map[string]bool)
	purgedPhotos = make(map[string]bool)
	originalsPath := prg.originalsPath()

	if err := mutex.Worker.Start(); err != nil {
		err = fmt.Errorf("purge: %s", err.Error())
		event.Error(err.Error())
		return purgedFiles, purgedPhotos, err
	}

	defer func() {
		mutex.Worker.Stop()
		runtime.GC()
	}()

	q := query.New(prg.conf.Db())

	limit := 500
	offset := 0

	for {
		files, err := q.ExistingFiles(limit, offset, opt.Path)

		if err != nil {
			return purgedFiles, purgedPhotos, err
		}

		if len(files) == 0 {
			break
		}

		for _, file := range files {
			if mutex.Worker.Canceled() {
				return purgedFiles, purgedPhotos, errors.New("purge canceled")
			}

			fileName := path.Join(prg.conf.OriginalsPath(), file.FileName)

			if ignore[fileName] || purgedFiles[fileName] {
				continue
			}

			if !fs.FileExists(fileName) {
				if opt.Dry {
					purgedFiles[fileName] = true
					log.Infof("purge: file %s would be removed", txt.Quote(fs.RelativeName(fileName, originalsPath)))
					continue
				}

				if err := file.Purge(); err != nil {
					log.Errorf("purge: %s", err)
				} else {
					purgedFiles[fileName] = true
					log.Infof("purge: removed file %s", txt.Quote(fs.RelativeName(fileName, originalsPath)))
				}
			}
		}

		if mutex.Worker.Canceled() {
			return purgedFiles, purgedPhotos, errors.New("purge canceled")
		}

		offset += limit
	}

	limit = 500
	offset = 0

	for {
		photos, err := q.MissingPhotos(limit, offset)

		if err != nil {
			return purgedFiles, purgedPhotos, err
		}

		if len(photos) == 0 {
			break
		}

		for _, photo := range photos {
			if mutex.Worker.Canceled() {
				return purgedFiles, purgedPhotos, errors.New("purge canceled")
			}

			if purgedPhotos[photo.PhotoUUID] {
				continue
			}

			if opt.Dry {
				purgedPhotos[photo.PhotoUUID] = true
				log.Infof("purge: photo %s would be removed", txt.Quote(photo.PhotoName))
				continue
			}

			if err := photo.Delete(opt.Hard); err != nil {
				log.Errorf("purge: %s", err)
			} else {
				purgedPhotos[photo.PhotoUUID] = true

				if opt.Hard {
					log.Infof("purge: permanently deleted photo %s", txt.Quote(photo.PhotoName))
				} else {
					log.Infof("purge: removed photo %s", txt.Quote(photo.PhotoName))
				}
			}
		}

		if mutex.Worker.Canceled() {
			return purgedFiles, purgedPhotos, errors.New("purge canceled")
		}

		offset += limit
	}

	return purgedFiles, purgedPhotos, err
}

// Cancel stops the current purge operation.
func (prg *Purge) Cancel() {
	mutex.Worker.Cancel()
}
