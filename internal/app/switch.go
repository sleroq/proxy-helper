package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sleroq/sb/internal/files"
)

type activeRecord struct {
	Backend string `json:"backend"`
}

type backendSwitch struct {
	settings                                         Settings
	activePath, configPath, manifestPath             string
	previousActive, previousConfig, previousManifest []byte
	activeMissing                                    bool
	oldName                                          string
}

func newBackendSwitch(s Settings) (*backendSwitch, error) {
	tx := &backendSwitch{
		settings: s, activePath: filepath.Join(s.StateDir, "active-backend.json"),
		configPath: s.ConfigPath(), manifestPath: filepath.Join(s.StateDir, "subscription.json"),
	}
	var err error
	tx.previousActive, err = os.ReadFile(tx.activePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	tx.activeMissing = os.IsNotExist(err)
	tx.oldName = s.Backend
	if tx.oldName == "" {
		tx.oldName = "sing-box"
	}
	if !tx.activeMissing {
		var record activeRecord
		if err := json.Unmarshal(tx.previousActive, &record); err != nil {
			return nil, err
		}
		tx.oldName = record.Backend
	}
	tx.previousConfig, err = os.ReadFile(tx.configPath)
	if err != nil {
		return nil, fmt.Errorf("installed config required: %w", err)
	}
	tx.previousManifest, err = os.ReadFile(tx.manifestPath)
	if err != nil {
		return nil, fmt.Errorf("installed manifest required: %w", err)
	}
	return tx, nil
}

func (tx *backendSwitch) install(config json.RawMessage, manifest Manifest, name string) error {
	if err := files.Write(tx.configPath, config, 0600); err != nil {
		return err
	}
	if err := files.Write(tx.manifestPath, manifest, 0644); err != nil {
		return err
	}
	return files.Write(tx.activePath, activeRecord{Backend: name}, 0600)
}

func (tx *backendSwitch) restore() error {
	var failures []error
	if err := files.WriteBytes(tx.configPath, tx.previousConfig, 0600); err != nil {
		failures = append(failures, err)
	}
	if err := files.WriteBytes(tx.manifestPath, tx.previousManifest, 0644); err != nil {
		failures = append(failures, err)
	}
	if tx.activeMissing {
		if err := os.Remove(tx.activePath); err != nil && !os.IsNotExist(err) {
			failures = append(failures, err)
		}
	} else if err := files.WriteBytes(tx.activePath, tx.previousActive, 0600); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (tx *backendSwitch) restart(ctx context.Context) error {
	args := tx.settings.RestartCommand
	return exec.CommandContext(ctx, args[0], args[1:]...).Run()
}

func (tx *backendSwitch) rollback(ctx context.Context, cause error, restartAttempted bool) error {
	restored := tx.restore()
	if restartAttempted {
		// An interrupted switch still needs a chance to restore the prior core.
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if err := tx.restart(recovery); err != nil {
			restored = errors.Join(restored, fmt.Errorf("prior service restart: %w", err))
		}
	}
	if restored != nil {
		return fmt.Errorf("switch failed: %w; rollback failed: %w", cause, restored)
	}
	return fmt.Errorf("switch failed (restored %s): %w", tx.oldName, cause)
}

func waitBackend(ctx context.Context, target Backend, apiURL string) error {
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		err := target.Ready(deadline, apiURL)
		if err == nil {
			return nil
		}
		if deadline.Err() != nil {
			return fmt.Errorf("target backend readiness: %w", err)
		}
		select {
		case <-deadline.Done():
			return fmt.Errorf("target backend readiness: %w", err)
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func checkSwitchPermissions(s Settings) error {
	if len(s.RestartCommand) == 0 {
		return fmt.Errorf("backend switching requires restart_command")
	}
	info, err := os.Stat(s.StateDir)
	if err != nil {
		return err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("backend switching requires state-dir owner permission")
	}
	return nil
}

func Switch(ctx context.Context, settings Settings, target Manager) error {
	s := settings
	name := target.Backend.Name()
	if err := checkSwitchPermissions(s); err != nil {
		return err
	}
	lock, err := s.Lock()
	if err != nil {
		return fmt.Errorf("backend switching requires state-dir owner permission: %w", err)
	}
	defer func() { _ = lock.Close() }()
	tx, err := newBackendSwitch(s)
	if err != nil {
		return err
	}
	if tx.oldName == name {
		return nil
	}
	candidate, manifest, err := target.Candidate(ctx)
	if err != nil {
		return err
	}
	if err := tx.install(candidate, manifest, name); err != nil {
		return tx.rollback(ctx, err, false)
	}
	if err := tx.restart(ctx); err != nil {
		return tx.rollback(ctx, err, true)
	}
	if err := waitBackend(ctx, target.Backend, s.APIURL); err != nil {
		return tx.rollback(ctx, err, true)
	}
	return nil
}
