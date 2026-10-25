package server

import (
	"database/sql"
	"time"
)

type Service struct {
	db            *sql.DB
	now           func() time.Time
	geoIPProvider GeoIPProvider
}

func NewService(db *sql.DB, providers ...GeoIPProvider) *Service {
	var provider GeoIPProvider
	if len(providers) != 0 {
		provider = providers[0]
	}
	return &Service{
		db:            db,
		now:           time.Now,
		geoIPProvider: provider,
	}
}
