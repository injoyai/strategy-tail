package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type cursorState struct {
	Version int              `json:"version"`
	Servers map[string]int64 `json:"servers"`
}

func loadCursors(path string) (cursorState, error) {
	state := cursorState{Version: 1, Servers: make(map[string]int64)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		// Legacy clients treated a corrupt checkpoint as zero and rewrote it
		// after the next event. Preserve that recovery behavior.
		return cursorState{Version: 1, Servers: make(map[string]int64)}, nil
	}
	if state.Version != 1 || state.Servers == nil {
		return state, fmt.Errorf("提醒进度格式无效")
	}
	for _, id := range state.Servers {
		if id < 0 {
			return state, fmt.Errorf("提醒进度不能为负数")
		}
	}
	return state, nil
}

func readServerCursor(path, server string) (int64, bool) {
	key, err := eventsURL(server, 0)
	if err != nil {
		return 0, false
	}
	state, err := loadCursors(path)
	if err != nil {
		return 0, false
	}
	id, ok := state.Servers[key]
	return id, ok
}

func writeServerCursor(path, server string, id int64) error {
	key, err := eventsURL(server, 0)
	if err != nil {
		return err
	}
	state, err := loadCursors(path)
	if err != nil {
		return err
	}
	state.Servers[key] = id
	return saveCursors(path, state)
}

// Upgrade the old scalar checkpoint once, before starting the connection loop.
// The saved address owns that checkpoint. Without a saved address, bind it to
// the startup address because the legacy format carried no server identity.
func migrateCursor(path, savedServer, startupServer string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	id, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || id < 0 {
		_, err = loadCursors(path)
		return err
	}
	server := savedServer
	if _, err := eventsURL(server, 0); err != nil {
		server = startupServer
	}
	key, err := eventsURL(server, 0)
	if err != nil {
		return err
	}
	return saveCursors(path, cursorState{Version: 1, Servers: map[string]int64{key: id}})
}

func saveCursors(path string, state cursorState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".cursor-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}
