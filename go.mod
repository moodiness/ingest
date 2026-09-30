module github.com/moodiness/ingest

go 1.26.8

require (
	filippo.io/age v1.3.2
	github.com/fsnotify/fsnotify v1.9.0
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761
	github.com/jackc/pgx/v5 v5.10.0
	github.com/moodiness/ingest/torznab v0.0.0
	github.com/robfig/cron/v3 v3.0.1
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/crypto v0.57.0
	golang.org/x/net v0.59.0
)

require (
	filippo.io/hpke v0.4.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)

replace github.com/moodiness/ingest/torznab => ./torznab
