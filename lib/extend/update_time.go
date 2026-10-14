package extend

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LastSuccessfulUpdate reads the batch marker shared by CLI and Web updates.
func (this *PullKline) LastSuccessfulUpdate() (time.Time, error) {
	filename, err := filepath.Abs(filepath.Join(this.Config.Dir, "update.db"))
	if err != nil {
		return time.Time{}, err
	}
	if _, err := os.Stat(filename); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	uri := url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(filename), "/"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return time.Time{}, err
	}
	defer db.Close()
	var timestamp int64
	err = db.QueryRow(`SELECT "Time" FROM "update" WHERE "Key" = ?`, "pull").Scan(&timestamp)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	if timestamp <= 0 {
		return time.Time{}, nil
	}
	return time.Unix(timestamp, 0), nil
}
