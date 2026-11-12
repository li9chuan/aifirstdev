package logging

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// recoverStatCSV runs before rotation or opening the append writer. A record is
// committed only if its CSV syntax and final newline are complete. Preserve the
// remaining bytes before truncating so interrupted records are inspectable.
func recoverStatCSV(path string) (result error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open Stat CSV for recovery: %w", err)
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Stat CSV requires a regular file: %s", path)
	}
	if info.Size() == 0 {
		return nil
	}
	var last [1]byte
	if _, err := file.ReadAt(last[:], info.Size()-1); err != nil {
		return fmt.Errorf("read Stat CSV terminator: %w", err)
	}
	end, err := statCSVBoundary(file, info.Size(), last[0] == '\n')
	if err != nil {
		return fmt.Errorf("scan Stat CSV recovery boundary: %w", err)
	}
	if end == info.Size() {
		return nil
	}
	quarantine, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".corrupt-*")
	if err != nil {
		return fmt.Errorf("preserve incomplete Stat CSV tail: %w", err)
	}
	name := quarantine.Name()
	_, copyErr := io.CopyN(quarantine, io.NewSectionReader(file, end, info.Size()-end), info.Size()-end)
	preserveErr := errors.Join(copyErr, quarantine.Sync(), quarantine.Close())
	if preserveErr != nil {
		return errors.Join(fmt.Errorf("preserve incomplete Stat CSV tail at %s: %w", name, preserveErr), os.Remove(name))
	}
	if err := file.Truncate(end); err != nil {
		return fmt.Errorf("truncate Stat CSV after preserving tail at %s: %w", name, err)
	}
	// Recovery must not make yesterday's valid prefix appear to be today's log.
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		return fmt.Errorf("restore recovered Stat CSV modification time: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync recovered Stat CSV: %w", err)
	}
	reportLocalError(fmt.Errorf("recovered Stat CSV %q: preserved %d tail bytes at %q", path, info.Size()-end, name))
	return nil
}

// statCSVBoundary accepts variable column counts and quoted multiline fields.
// Syntax damage is isolated from the last complete record; read errors fail
// initialization without rewriting the file.
func statCSVBoundary(input io.Reader, size int64, terminated bool) (int64, error) {
	reader := csv.NewReader(input)
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = true
	var end int64
	for {
		_, err := reader.Read()
		if errors.Is(err, io.EOF) {
			if reader.InputOffset() != size {
				return 0, io.ErrUnexpectedEOF
			}
			if terminated {
				return size, nil
			}
			return end, nil
		}
		if errors.Is(err, csv.ErrBareQuote) || errors.Is(err, csv.ErrQuote) {
			return end, nil
		}
		if err != nil {
			return 0, err
		}
		next := reader.InputOffset()
		if next == size && !terminated {
			return end, nil
		}
		end = next
	}
}
