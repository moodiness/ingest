package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/ingest/internal/model"
)

func decodeObject(data []byte) (map[string]any, error) {
	value := make(map[string]any)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New("stored structured fields are invalid")
	}
	if value == nil {
		value = make(map[string]any)
	}
	return value, nil
}

const rawColumns = `record.id,record.run_id,record.provider_id,record.source_id,record.page_index,record.content_type,
COALESCE(payload.fields,record.fields),record.error,record.ignored,record.auxiliary,record.created_at,record.page_id,record.payload_retained`

const rawSource = ` FROM ingest.raw_records AS record
LEFT JOIN ingest.raw_records AS payload ON payload.id=record.payload_id`

func scanRaw(row scanner, includeBytes bool) (model.RawRecord, error) {
	var record model.RawRecord
	var fields []byte
	values := []any{&record.ID, &record.RunID, &record.ProviderID, &record.SourceID, &record.Page, &record.ContentType, &fields, &record.Error, &record.Ignored, &record.Auxiliary, &record.CreatedAt, &record.PageID, &record.PayloadRetained}
	if includeBytes {
		values = append(values, &record.Raw)
	}
	if err := row.Scan(values...); err != nil {
		return record, err
	}
	var err error
	record.Fields, err = decodeObject(fields)
	return record, err
}

func (s *Store) ListRaw(ctx context.Context, opts model.ListOptions) (model.List[model.RawRecord], error) {
	opts = pageOptions(opts)
	result := model.List[model.RawRecord]{Items: []model.RawRecord{}, Limit: opts.Limit, Offset: opts.Offset}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, databaseError("begin raw record listing", err)
	}
	defer rollback(tx)
	const filter = rawSource + ` WHERE ($1='' OR record.provider_id=$1) AND ($2='' OR record.run_id=$2)
AND ($3='' OR strpos(lower(record.source_id),lower($3))>0 OR strpos(lower(COALESCE(payload.fields,record.fields)::text),lower($3))>0 OR strpos(lower(record.error),lower($3))>0)`
	if err := tx.QueryRow(ctx, "SELECT count(*)"+filter, opts.ProviderID, opts.RunID, opts.Query).Scan(&result.Total); err != nil {
		return result, databaseError("count raw records", err)
	}
	rows, err := tx.Query(ctx, "SELECT "+rawColumns+filter+" ORDER BY record.id DESC LIMIT $4 OFFSET $5", opts.ProviderID, opts.RunID, opts.Query, opts.Limit, opts.Offset)
	if err != nil {
		return result, databaseError("list raw records", err)
	}
	defer rows.Close()
	for rows.Next() {
		record, err := scanRaw(rows, false)
		if err != nil {
			return result, databaseError("read raw record metadata", err)
		}
		result.Items = append(result.Items, record)
	}
	if err := rows.Err(); err != nil {
		return result, databaseError("list raw records", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return result, databaseError("finish raw record listing", err)
	}
	return result, nil
}

func (s *Store) Raw(ctx context.Context, id int64) (model.RawRecord, error) {
	record, err := scanRaw(s.pool.QueryRow(ctx, "SELECT "+rawColumns+",COALESCE(payload.raw,record.raw)"+rawSource+" WHERE record.id=$1", id), true)
	return record, databaseError("read raw record", err)
}

func (s *Store) RawPage(ctx context.Context, id int64) ([]byte, string, error) {
	var body []byte
	var contentType string
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(payload.body,page.body),page.content_type
FROM ingest.pages AS page LEFT JOIN ingest.pages AS payload ON payload.id=page.payload_id
WHERE page.id=$1 AND page.payload_retained`, id).Scan(&body, &contentType)
	return body, contentType, databaseError("read response page", err)
}

const torrentColumns = `provider_id,source_id,fields,raw_id,first_seen_at,last_seen_at,historical,origin,occurrence_id`

func scanTorrent(row scanner) (model.Torrent, error) {
	var torrent model.Torrent
	var fields []byte
	var origin []byte
	if err := row.Scan(&torrent.ProviderID, &torrent.SourceID, &fields, &torrent.RawID, &torrent.FirstSeenAt, &torrent.LastSeenAt, &torrent.Historical, &origin, &torrent.OccurrenceID); err != nil {
		return torrent, err
	}
	if len(origin) != 0 {
		if err := json.Unmarshal(origin, &torrent.Origin); err != nil {
			return torrent, errors.New("stored catalogue provenance is invalid")
		}
	}
	var err error
	torrent.Fields, err = decodeObject(fields)
	return torrent, err
}

func (s *Store) ListTorrents(ctx context.Context, opts model.ListOptions) (model.List[model.Torrent], error) {
	var err error
	opts, err = torrentSearchOptions(opts)
	if err != nil {
		return model.List[model.Torrent]{}, err
	}
	result := model.List[model.Torrent]{Items: []model.Torrent{}, Limit: opts.Limit, Offset: opts.Offset}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, databaseError("begin torrent listing", err)
	}
	defer rollback(tx)
	filter, order, args := torrentSearchSQL(opts)
	if err := tx.QueryRow(ctx, "SELECT count(*)"+filter, args...).Scan(&result.Total); err != nil {
		return result, databaseError("count torrents", err)
	}
	limitParameter := "$" + strconv.Itoa(len(args)+1)
	offsetParameter := "$" + strconv.Itoa(len(args)+2)
	args = append(args, opts.Limit, opts.Offset)
	rows, err := tx.Query(ctx, "SELECT "+torrentColumns+filter+order+" LIMIT "+limitParameter+" OFFSET "+offsetParameter, args...)
	if err != nil {
		return result, databaseError("list torrents", err)
	}
	defer rows.Close()
	for rows.Next() {
		torrent, err := scanTorrent(rows)
		if err != nil {
			return result, databaseError("read torrent", err)
		}
		result.Items = append(result.Items, torrent)
	}
	if err := rows.Err(); err != nil {
		return result, databaseError("list torrents", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return result, databaseError("finish torrent listing", err)
	}
	return result, nil
}

func (s *Store) Overview(ctx context.Context) (model.Overview, error) {
	result := model.Overview{RecentRuns: []model.Run{}}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, databaseError("begin overview", err)
	}
	defer rollback(tx)
	if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM ingest.torrents),
(SELECT count(*) FROM ingest.raw_records),(SELECT count(*) FROM ingest.runs WHERE status IN ('queued','running'))`).Scan(&result.Torrents, &result.RawRecords, &result.ActiveRuns); err != nil {
		return result, databaseError("read overview counts", err)
	}
	rows, err := tx.Query(ctx, "SELECT "+runColumns+" FROM ingest.runs ORDER BY created_at DESC,id DESC LIMIT 10")
	if err != nil {
		return result, databaseError("read recent collections", err)
	}
	defer rows.Close()
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return result, databaseError("read recent collection", err)
		}
		result.RecentRuns = append(result.RecentRuns, run)
	}
	if err := rows.Err(); err != nil {
		return result, databaseError("read recent collections", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return result, databaseError("finish overview", err)
	}
	return result, nil
}
